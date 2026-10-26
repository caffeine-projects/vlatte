package mockgrpc

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/atomicfile"
	"github.com/caffeine-projects/vlatte/internal/bodyfile"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
)

var _ handler.Files = adminFiles{}

const (
	// _defaultSaveDir is where the admin API saves mocks by default, under the root directory.
	_defaultSaveDir    = "_mocks"
	_defaultSaveFormat = "yaml"
	_saveTempPattern   = ".save-*.tmp"
)

// adminFiles serves the mock files of a server to its admin API.
type adminFiles struct {
	*Server
}

// SaveMocks writes the mocks the admin API added as mock files, <id>.mock.<format>, into dir, then serves them from
// the files, which load from then on: each file's mock takes the place of the admin API's mock with its id, in the
// step that loads the files, so a call finds one of the two. A relative dir starts at the root directory. Every mock
// is described and checked first: when one can't be, nothing is written. When the files fail to load, they are put
// back as they were, and the admin API's mocks stay.
func (f adminFiles) SaveMocks(ctx context.Context, dir, format string) ([]string, error) {
	app := f.Server

	format = cmp.Or(format, _defaultSaveFormat)
	dir = cmp.Or(dir, _defaultSaveDir)

	if !filepath.IsAbs(dir) {
		dir = filepath.Join(app.config.RootDir, dir)
	}

	toWD, err := bodyfile.FromDir(dir)
	if err != nil {
		return nil, fmt.Errorf("server: save: %w", err)
	}

	mocks, err := app.storage.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	var (
		paths []string
		docs  [][]byte
	)

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

	written := make([]atomicfile.Saved, 0, len(paths))

	for i, path := range paths {
		file, err := atomicfile.Backup(path)
		if err == nil {
			err = atomicfile.Write(dir, _saveTempPattern, path, docs[i])
		}

		if err != nil {
			return nil, fmt.Errorf("server: save: %w", errors.Join(err, atomicfile.Restore(written, _saveTempPattern)))
		}

		written = append(written, file)
	}

	if err := app.loader.loadWith(app, dir); err != nil {
		return nil, errors.Join(err, atomicfile.Restore(written, _saveTempPattern))
	}

	return paths, nil
}

// mockFile describes mock as the mock file at path, in format, and checks that the file loads back. toWD leads from
// the file's directory to the working directory, which the relative body files of the admin API's mocks open from.
func mockFile(app *Server, mock *Mock, path, format, toWD string) ([]byte, error) {
	desc, err := mock.Describe().MarshalJSON()
	if err != nil {
		return nil, err
	}

	// A copy of the description, whose files move: MarshalJSON wrote it as a JSON object.
	var doc map[string]any
	_ = json.Unmarshal(desc, &doc)

	relocate(doc, toWD)

	data, err := config.Encode(doc, format)
	if err != nil {
		return nil, err
	}

	if _, err := (mockDoc{origin: "file " + path, source: path, format: format, dir: filepath.Dir(path), data: data}).build(app); err != nil {
		return nil, err
	}

	return data, nil
}

// relocate makes the relative body files of doc, a mock's description, which open from the working directory, open
// from the directory toWD leads from to it: those of its response, of the responses of its bidi reply, and of its
// webhooks and their multipart parts.
func relocate(doc map[string]any, toWD string) {
	bodyfile.Relocate(doc["response"], toWD)

	bidi, _ := doc["bidi"].(map[string]any)
	bodyfile.Relocate(bidi["otherwise"], toWD)
	bodyfile.Relocate(bidi["on_end"], toWD)

	rules, _ := bidi["on"].([]any)
	for _, rule := range rules {
		r, _ := rule.(map[string]any)
		bodyfile.Relocate(r["response"], toWD)
	}

	// The mock file format reads the parameters of webhook only: other listeners read theirs as they will.
	events, _ := doc["on_complete"].([]any)
	for _, item := range events {
		event, _ := item.(map[string]any)
		if event["name"] != "webhook" {
			continue
		}

		params, _ := event["parameters"].(map[string]any)
		bodyfile.Relocate(params, toWD)

		for _, key := range []string{"multipart", "multipart_template"} {
			multipart, _ := params[key].(map[string]any)
			parts, _ := multipart["parts"].([]any)

			for _, part := range parts {
				bodyfile.Relocate(part, toWD)
			}
		}
	}
}

// adminRequestPattern returns the filter of the request part of a mock file, v, the request key of a query of the
// admin API: its keys are a mock file's, and it matches as a mock does.
func (app *Server) adminRequestPattern(v config.Value) (journal.Filter, error) {
	def := newMockDef()
	doc := config.Value{Origin: v.Origin, Any: map[string]any{"request": v.Any}}

	problems := config.ProblemsOf(doc, _mockSchema.Decode(&def, doc))
	problems = append(problems, conflicts(v.Origin, "request", def.request.message)...)

	if def.request.method == "" {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: v.Origin, Key: "request.method",
			Message: "request.method is required", Hint: "name the method, such as pkg.Service/Method",
		})
	}

	if len(problems) > 0 {
		return nil, &config.Error{Problems: problems}
	}

	b := &Builder{enabled: true}
	def.apply(b)

	return app.RequestPattern(b)
}
