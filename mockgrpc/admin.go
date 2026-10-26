package mockgrpc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/atomicfile"
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
// the files, which load from then on: each file's mock takes the place of the admin API's mock with its id. A relative
// dir starts at the root directory. Every mock is described and checked first: when one can't be, nothing is written.
// When the files fail to load, they are put back as they were, and so are the admin API's mocks.
func (f adminFiles) SaveMocks(ctx context.Context, dir, format string) ([]string, error) {
	app := f.Server

	format = cmp.Or(format, _defaultSaveFormat)
	dir = cmp.Or(dir, _defaultSaveDir)

	if !filepath.IsAbs(dir) {
		dir = filepath.Join(app.config.RootDir, dir)
	}

	mocks, err := app.storage.PickAll(ctx)
	if err != nil {
		return nil, err
	}

	var (
		saved []*Mock
		paths []string
		docs  [][]byte
	)

	for _, mock := range mocks {
		if mock.Origin() != handler.Origin {
			continue
		}

		path := filepath.Join(dir, mock.ID()+".mock."+format)

		data, err := mockFile(app, mock, path, format)
		if err != nil {
			return nil, fmt.Errorf("%w: mock %q: %w", handler.ErrNotDescribed, mock.ID(), err)
		}

		saved = append(saved, mock)
		paths = append(paths, path)
		docs = append(docs, data)
	}

	if len(paths) == 0 {
		return paths, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("server: save: %w", err)
	}

	written := make([]savedFile, 0, len(paths))

	for i, path := range paths {
		file, err := backup(path)
		if err == nil {
			err = atomicfile.Write(dir, _saveTempPattern, path, docs[i])
		}

		if err != nil {
			return nil, fmt.Errorf("server: save: %w", errors.Join(err, restore(written)))
		}

		written = append(written, file)
	}

	ids := make([]string, len(saved))
	for i, m := range saved {
		ids[i] = m.ID()
	}

	if err := app.storage.Delete(ctx, ids...); err != nil {
		return nil, fmt.Errorf("server: save: %w", errors.Join(err, restore(written)))
	}

	if err := app.loader.loadWith(app, dir); err != nil {
		return nil, errors.Join(err, restore(written), app.storage.Store(ctx, saved...))
	}

	return paths, nil
}

// mockFile describes mock as the mock file at path, in format, and checks that the file loads back.
func mockFile(app *Server, mock *Mock, path, format string) ([]byte, error) {
	d := mock.Describe()

	doc, err := d.Marshallable()
	if err != nil {
		return nil, err
	}

	data, err := config.Encode(doc, format)
	if err != nil {
		return nil, err
	}

	if _, err := (mockDoc{origin: "file " + path, source: path, format: format, dir: filepath.Dir(path), data: data}).build(app); err != nil {
		return nil, err
	}

	return data, nil
}

// savedFile is a mock file SaveMocks wrote, and what it held before.
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

// restore puts files back as they were, the last written first. It returns what it couldn't put back.
func restore(files []savedFile) error {
	var errs []error

	for _, f := range slices.Backward(files) {
		var err error
		if f.existed {
			err = atomicfile.Write(filepath.Dir(f.path), _saveTempPattern, f.path, f.prev)
		} else if err = os.Remove(f.path); errors.Is(err, fs.ErrNotExist) {
			err = nil
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("putting %s back: %w", f.path, err))
		}
	}

	return errors.Join(errs...)
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
