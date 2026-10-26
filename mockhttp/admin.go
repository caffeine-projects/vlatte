package mockhttp

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/atomicfile"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// _mockFileSchema is the JSON Schema of mock files, which the admin API serves to the clients that write them.
//
//go:embed schema/http.schema.json
var _mockFileSchema []byte

var (
	_ adminapi.Recorder = adminRecorder{}
	_ adminapi.Files    = adminFiles{}
	_ RecordConfigurer  = recordDoc{}
	_ mockHTTPLifecycle = adminEvents{}
)

const (
	// _defaultSaveDir is where the admin API saves mocks by default: the default mock file patterns load its YAML
	// files on the next start too.
	_defaultSaveDir    = WorkDirVar + "/_mocks"
	_defaultSaveFormat = "yaml"

	// _saveTempPattern names the files a save is writing: never *.mock.*, so loading mock files skips them.
	_saveTempPattern = ".save-*.tmp"
)

// adminLogs returns the admin API's logs, and the writer the server's logger writes them through, when the server
// serves the admin API and builds its logger: a logger it was given writes where it was told to.
func adminLogs(conf *Config) (*adminapi.Logs, io.Writer) {
	if conf.Admin == nil || conf.Logger != nil {
		return nil, nil
	}

	logs := adminapi.NewLogs()

	return logs, logs
}

// withAdminEvents adds the admin API's events to lifecycle, when the server serves the admin API: no event is
// published without it.
func withAdminEvents(conf *Config, lifecycle mockHTTPLifecycle) (*adminapi.Events, mockHTTPLifecycle) {
	if conf.Admin == nil {
		return nil, lifecycle
	}

	events := adminapi.NewEvents()

	return events, lifecycles{lifecycle, adminEvents{events}}
}

// adminEvents publishes what the server serves to the admin API's events, which GET /events streams.
type adminEvents struct {
	events *adminapi.Events
}

func (a adminEvents) OnRequest(*RequestValues) {}

func (a adminEvents) OnMatch(r *RequestValues, res *Response) {
	a.publish(adminapi.EventMatched, r, r.answered(), nil)
}

func (a adminEvents) OnProxied(r *RequestValues, res *Response) {
	a.publish(adminapi.EventProxied, r, r.answered(), nil)
}

func (a adminEvents) OnNoMatch(r *RequestValues) {
	a.publish(adminapi.EventUnmatched, r, r.answered(), nil)
}

func (a adminEvents) OnWarning(r *RequestValues, err error) {
	a.publish(adminapi.EventWarning, r, 0, err)
}

func (a adminEvents) OnError(r *RequestValues, err error) {
	a.publish(adminapi.EventError, r, r.answered(), err)
}

// publish publishes the event of r, with the status the client got, 0 for none.
func (a adminEvents) publish(kind string, r *RequestValues, status int, err error) {
	e := adminapi.Event{
		Type:      kind,
		Time:      time.Now(),
		RequestID: r.ReqID,
		Method:    r.RawRequest.Method,
		Target:    r.URL.String(),
		Status:    status,
	}
	if r.Mock != nil {
		e.MockID = r.Mock.ID()
	}

	if err != nil {
		e.Message = err.Error()
	}

	a.events.Publish(e)
}

// adminRecorder lets the admin API record: it starts recordings with the settings of a document.
type adminRecorder struct {
	*Server
}

// StartRecording starts a recording, as Server.StartRecording does, with the settings of doc, the record section of
// the server's settings, in format.
func (r adminRecorder) StartRecording(doc []byte, format string) error {
	return r.Server.StartRecording(recordDoc{data: doc, format: format})
}

// recordDoc applies the settings of a document to a recording: the keys of the record section of the server's
// settings, such as the body of the admin API's POST /recording. Settings that fail the recording's checks are
// problems of the document too.
type recordDoc struct {
	data   []byte
	format string
}

func (d recordDoc) Apply(c *RecordConfig) error {
	if strings.TrimSpace(string(d.data)) == "" {
		return nil
	}

	if _, err := _recordSchema().Load(c, []config.Source{config.Bytes("admin request", d.format, d.data)}); err != nil {
		return err
	}

	if err := c.clone().check(); err != nil {
		return &config.Error{Scope: "recording", Problems: []config.Problem{{
			Code: config.CodeInvalidValue, Origin: "admin request", Message: err.Error(),
		}}}
	}

	return nil
}

// adminFiles lets the admin API load the server's mock files again, and save the mocks it added as mock files.
type adminFiles struct {
	*Server
}

// SaveMocks writes the mocks the admin API added as mock files, <id>.mock.<format>, into dir, then serves them from
// the files: the server loads its mock files again, dir's too, and each file's mock takes the place of the admin API's
// mock with its id. A relative dir starts at root_dir. Every mock is described and checked first: when one can't be,
// nothing is written. When the files fail to load, they are put back as they were. It returns the paths of the files.
func (f adminFiles) SaveMocks(ctx context.Context, dir, format string) ([]string, error) {
	app := f.Server

	// The admin API checked the format.
	format = cmp.Or(format, _defaultSaveFormat)
	// A dir the admin API sends stays inside root_dir, as it checked: only the default is in the work directory.
	switch {
	case dir == "":
		dir = app.config.path(_defaultSaveDir)
	case !filepath.IsAbs(dir):
		dir = filepath.Join(app.config.RootDir, dir)
	}

	// Descriptions name relative body files from the working directory; a mock file names them from its directory.
	toWD, err := fromDir(dir)
	if err != nil {
		return nil, fmt.Errorf("server: save: %w", err)
	}

	mocks, err := app.storage.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	var paths []string
	var docs [][]byte

	for _, mock := range mocks {
		if mock.Origin() != adminapi.Origin {
			continue
		}

		path := filepath.Join(dir, mock.ID()+".mock."+format)

		data, err := mockFile(app, mock, path, format, toWD)
		if err != nil {
			return nil, fmt.Errorf("%w: mock %q: %w", adminapi.ErrNotDescribed, mock.ID(), err)
		}

		paths = append(paths, path)
		docs = append(docs, data)
	}

	if len(paths) == 0 {
		return paths, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("server: save: %w", err)
	}

	// Files that fail to load, such as one with the id of another mock file, would fail every later start: they are
	// put back as they were.
	written := make([]savedFile, 0, len(paths))

	for i, path := range paths {
		file, err := backup(path)
		if err == nil {
			err = atomicfile.Write(filepath.Dir(path), _saveTempPattern, path, docs[i])
		}

		if err != nil {
			return nil, fmt.Errorf("server: save: %w", errors.Join(err, restore(written, _saveTempPattern)))
		}

		written = append(written, file)
	}

	if err := app.loadSaved(dir); err != nil {
		return nil, errors.Join(err, restore(written, _saveTempPattern))
	}

	return paths, nil
}

// savedFile is a mock file a change wrote or removed, and what it held before.
type savedFile struct {
	path    string
	prev    []byte
	existed bool
}

// backup returns what the file at path holds now: its content, or that there is none.
func backup(path string) (savedFile, error) {
	prev, err := os.ReadFile(path)

	switch {
	case err == nil:
		return savedFile{path: path, prev: prev, existed: true}, nil
	case errors.Is(err, fs.ErrNotExist):
		return savedFile{path: path}, nil
	default:
		return savedFile{}, err
	}
}

// restore puts files back as they were, the last changed first: it writes back what they held, through temporary
// files named after pattern, and removes those that didn't exist. It returns what it couldn't put back.
func restore(files []savedFile, pattern string) error {
	var errs []error

	for _, f := range slices.Backward(files) {
		var err error
		if f.existed {
			err = atomicfile.Write(filepath.Dir(f.path), pattern, f.path, f.prev)
		} else if err = os.Remove(f.path); errors.Is(err, fs.ErrNotExist) {
			err = nil
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("putting %s back: %w", f.path, err))
		}
	}

	return errors.Join(errs...)
}

// mockFile describes mock as the mock file at path, in format, and checks that the file loads back. toWD leads from
// the file's directory to the working directory.
func mockFile(app *Server, mock *Mock, path, format, toWD string) ([]byte, error) {
	doc, err := value.ViaJSON(mock)
	if err != nil {
		return nil, err
	}

	relocate(doc, toWD)

	data, err := config.Encode(doc, format)
	if err != nil {
		return nil, err
	}

	built, err := mockDoc{origin: "file " + path, source: path, format: format, dir: filepath.Dir(path), data: data}.build(app)
	if err != nil {
		return nil, err
	}

	_ = built.Close()

	return data, nil
}

// fromDir returns the path that leads from dir to the working directory, such as ../.., or "" when they are one.
func fromDir(dir string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(abs, wd)
	if err != nil || rel == "." {
		return "", err
	}

	return rel, nil
}

// relocate makes the relative body files of doc, a mock's description, which open from the working directory, open
// from the directory toWD leads from to it: it puts toWD before them. The names stay as they are otherwise, since they
// can be templates. Only the files of the mock file format move: those of replies and their multipart parts, of form
// file patterns, and of webhooks. Other keys may be named body_file too, such as in a template's data or another
// listener's parameters, and stay as they are.
func relocate(doc any, toWD string) {
	mock, ok := doc.(map[string]any)
	if !ok || toWD == "" {
		return
	}

	replies := []any{mock["response"]}
	if seq, ok := mock["response_sequence"].(map[string]any); ok {
		replies = append(replies, listItems(seq["responses"])...)
		replies = append(replies, seq["after_ended"])
	}

	if random, ok := mock["response_random"].(map[string]any); ok {
		replies = append(replies, listItems(random["responses"])...)
	}

	for _, reply := range replies {
		relocateFile(reply, toWD)
		relocateParts(reply, "multipart", toWD)
	}

	if req, ok := mock["request"].(map[string]any); ok {
		files, _ := req["form_file"].(map[string]any)
		for _, pattern := range files {
			relocateFile(pattern, toWD)

			for _, p := range listItems(pattern) {
				relocateFile(p, toWD)
			}
		}
	}

	// The mock file format reads the parameters of webhook only: other listeners read theirs as they will.
	for _, item := range listItems(mock["on_complete"]) {
		event, _ := item.(map[string]any)
		if event["name"] != "webhook" {
			continue
		}

		relocateFile(event["parameters"], toWD)
		relocateParts(event["parameters"], "multipart", toWD)
		relocateParts(event["parameters"], "multipart_template", toWD)
	}
}

// relocateParts relocates the body files of the parts of the multipart body of obj under key, as relocate does.
func relocateParts(obj any, key, toWD string) {
	m, _ := obj.(map[string]any)
	multipart, _ := m[key].(map[string]any)

	for _, part := range listItems(multipart["parts"]) {
		relocateFile(part, toWD)
	}
}

// relocateFile relocates the body_file of obj, an object, when it names a relative file, as relocate does.
func relocateFile(obj any, toWD string) {
	m, _ := obj.(map[string]any)
	if name, ok := m["body_file"].(string); ok && name != "" && !filepath.IsAbs(name) {
		m["body_file"] = toWD + string(filepath.Separator) + name
	}
}

// listItems returns the items of v, when it is a list.
func listItems(v any) []any {
	list, _ := v.([]any)
	return list
}

// loadSaved loads the mock files again, those of dir too, from now on. When they fail to load, dir stays out.
func (app *Server) loadSaved(dir string) error {
	app.recordMu.Lock()
	known := slices.Contains(app.saved, dir)
	if !known {
		app.saved = append(app.saved, dir)
	}
	app.recordMu.Unlock()

	err := app.files.Load(app)
	if err != nil && !known {
		app.recordMu.Lock()
		app.saved = slices.DeleteFunc(app.saved, func(d string) bool { return d == dir })
		app.recordMu.Unlock()
	}

	return err
}

// savedDirs returns the directories the admin API saved mocks into.
func (app *Server) savedDirs() []string {
	app.recordMu.Lock()
	defer app.recordMu.Unlock()

	return slices.Clone(app.saved)
}
