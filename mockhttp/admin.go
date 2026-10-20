package mockhttp

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ handler.Recorder  = adminRecorder{}
	_ handler.Files     = adminFiles{}
	_ RecordConfigurer  = recordDoc{}
	_ mockHTTPLifecycle = adminEvents{}
)

const (
	// _defaultSaveDir is where the admin API saves mocks by default: the default mock file patterns load its YAML
	// files on the next start too.
	_defaultSaveDir    = "testdata/_mocks"
	_defaultSaveFormat = "yaml"
)

// withAdminEvents adds the admin API's events to lifecycle, when the server serves the admin API: no event is
// published without it.
func withAdminEvents(conf *Config, lifecycle mockHTTPLifecycle) (*handler.Events, mockHTTPLifecycle) {
	if conf.Admin == nil {
		return nil, lifecycle
	}

	events := handler.NewEvents()

	return events, lifecycles{lifecycle, adminEvents{events}}
}

// adminEvents publishes what the server serves to the admin API's events, which GET /events streams.
type adminEvents struct {
	events *handler.Events
}

func (a adminEvents) OnRequest(*RequestValues) {}

func (a adminEvents) OnMatch(r *RequestValues, res *Response) {
	a.publish(handler.EventMatched, r, res, nil)
}

func (a adminEvents) OnProxied(r *RequestValues, res *Response) {
	a.publish(handler.EventProxied, r, res, nil)
}

func (a adminEvents) OnNoMatch(r *RequestValues) {
	a.publish(handler.EventUnmatched, r, nil, nil)
}

func (a adminEvents) OnWarning(r *RequestValues, err error) {
	a.publish(handler.EventWarning, r, nil, err)
}

func (a adminEvents) OnError(r *RequestValues, err error) {
	a.publish(handler.EventError, r, nil, err)
}

func (a adminEvents) publish(kind string, r *RequestValues, res *Response, err error) {
	e := handler.Event{Type: kind, Time: time.Now(), RequestID: r.ReqID, Method: r.RawRequest.Method, Target: r.URL.String()}
	if r.Mock != nil {
		e.MockID = r.Mock.ID()
	}

	if res != nil {
		e.Status = res.StatusCode
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
// nothing is written. It returns the paths of the files.
func (f adminFiles) SaveMocks(ctx context.Context, dir, format string) ([]string, error) {
	app := f.Server

	format, ok := config.Format(cmp.Or(format, _defaultSaveFormat))
	if !ok {
		return nil, fmt.Errorf("server: save: format %q is not one of %s", format, strings.Join(MockFileFormats(), ", "))
	}

	dir = cmp.Or(dir, _defaultSaveDir)
	if !filepath.IsAbs(dir) {
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
		if mock.Origin() != handler.Origin {
			continue
		}

		path := filepath.Join(dir, mock.ID()+".mock."+format)

		data, err := mockFile(app, mock, path, format, toWD)
		if err != nil {
			return nil, fmt.Errorf("%w: mock %q: %w", handler.ErrNotDescribed, mock.ID(), err)
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

	for i, path := range paths {
		if err := writeAtomically(path, docs[i]); err != nil {
			return nil, fmt.Errorf("server: save: %w", err)
		}
	}

	return paths, app.loadSaved(dir)
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

// relocate makes the relative body files a description names, from the working directory, open from the directory
// toWD leads from: it puts toWD before them. The names stay as they are otherwise, since they can be templates.
func relocate(doc any, toWD string) {
	switch v := doc.(type) {
	case map[string]any:
		for key, item := range v {
			if name, ok := item.(string); ok && key == "body_file" && toWD != "" && name != "" && !filepath.IsAbs(name) {
				v[key] = toWD + string(filepath.Separator) + name
				continue
			}

			relocate(item, toWD)
		}
	case []any:
		for _, item := range v {
			relocate(item, toWD)
		}
	}
}

// writeAtomically writes data to path through a temporary file of its directory, renamed into place: a reader finds the
// file whole, or not at all. The temporary file's name isn't a mock file's.
func writeAtomically(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".save-*.tmp")
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(f.Name(), path)
	}

	if err != nil {
		_ = os.Remove(f.Name())
	}

	return err
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
