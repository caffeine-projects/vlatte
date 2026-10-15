package mockhttp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var (
	_ vlatte.Builder[*Mock, *Server] = (*fileBuilder)(nil)
	_ vlatte.Builder[*Mock, *Server] = (*bytesBuilder)(nil)
)

// MockFileFormats lists the formats of mock files and recordings, as file extensions without the dot.
func MockFileFormats() []string {
	return []string{"json", "yaml", "yml"}
}

// mockFileFormat normalizes a format or an extension, such as ".YAML", and reports whether mock files can use it.
func mockFileFormat(format string) (string, bool) {
	return config.Format(format)
}

type fileBuilder struct {
	path string
}

// FromFile builds a mock from a JSON or YAML file, chosen by its extension: .json, .yaml or .yml.
//
// The file is data: only the settings that are templates, such as a templated reply body, are compiled, with the
// template engine of the server. Templates read the server's parameters, data and URL when a request arrives.
// A relative body_file opens from the file's directory.
// When the file has problems, the error is a list of every one, each with its key and a hint.
func FromFile(path string) vlatte.Builder[*Mock, *Server] {
	return &fileBuilder{path: path}
}

func (b *fileBuilder) Build(app *Server) (*Mock, error) {
	origin := "file " + b.path

	format, ok := mockFileFormat(filepath.Ext(b.path))
	if !ok {
		return nil, fail(config.Problem{
			Code: config.CodeUnsupportedFormat, Origin: origin,
			Message: fmt.Sprintf("mock file %q is not JSON or YAML", b.path), Hint: "use a .json, .yaml or .yml file",
		})
	}

	data, err := os.ReadFile(b.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fail(config.Problem{
			Code: config.CodeFileNotFound, Origin: origin,
			Message: fmt.Sprintf("mock file %q does not exist", b.path), Hint: workingDirHint(b.path),
		})
	case err != nil:
		return nil, fail(config.Problem{Code: config.CodeReadError, Origin: origin, Message: err.Error()})
	}

	doc := mockDoc{origin: origin, source: b.path, format: format, dir: filepath.Dir(b.path), data: data}

	return doc.build(app)
}

type bytesBuilder struct {
	data   []byte
	format string
}

// FromBytes builds a mock from a JSON or YAML document. format is json, yaml or yml.
// Like a mock file, the document is data; see FromFile.
// A relative body_file opens from the working directory.
func FromBytes(data []byte, format string) vlatte.Builder[*Mock, *Server] {
	return &bytesBuilder{data: data, format: format}
}

func (b *bytesBuilder) Build(app *Server) (*Mock, error) {
	return mockDoc{origin: "bytes", format: b.format, data: b.data}.build(app)
}

// buildAdminMock builds a mock posted to the admin API.
func buildAdminMock(app *Server, content []byte, format string) (*Mock, error) {
	return mockDoc{origin: "admin request", format: format, data: content}.build(app)
}

// mockDoc is a mock definition document and where it came from.
type mockDoc struct {
	origin string // where problems point, such as "file mocks/users.yaml"
	source string // the mock's source, a file path; empty for other documents
	format string // json, yaml or yml
	dir    string // where a relative body_file opens from; empty is the working directory
	data   []byte
}

// build turns the document into a mock for app. Every failure is a *config.Error listing each problem.
func (doc mockDoc) build(app *Server) (*Mock, error) {
	def := newMockDef()
	def.dir = doc.dir

	var problems []config.Problem
	if _, err := _mockSchema.Load(&def, []config.Source{config.Bytes(doc.origin, doc.format, doc.data)}); err != nil {
		cfgErr, ok := errors.AsType[*config.Error](err)
		if !ok {
			return nil, err
		}

		problems = cfgErr.Problems
	}

	if !parseFailed(problems) {
		problems = append(problems, def.check(doc.origin, problems)...)
	}

	if len(problems) > 0 {
		return nil, fail(problems...)
	}

	b := Request()
	def.apply(b, doc.dir)

	if doc.source != "" {
		b.SetSource(doc.source)
	}

	if def.ext != nil {
		for i, handler := range app.config.MockFileHandlers {
			if err := handler.Handle(def.ext, b); err != nil {
				problems = append(problems, config.Problem{
					Code: config.CodeInvalidValue, Origin: doc.origin, Key: "ext",
					Message: fmt.Sprintf("mock file handler %d: %v", i, err),
				})
			}
		}

		if len(problems) > 0 {
			return nil, fail(problems...)
		}
	}

	mock, err := b.Build(app)
	if err != nil {
		return nil, fail(config.Problem{Code: config.CodeInvalidValue, Origin: doc.origin, Message: err.Error()})
	}

	return mock, nil
}

// parseFailed reports whether the document couldn't be read at all, so checks between its keys would only add noise.
func parseFailed(problems []config.Problem) bool {
	return slices.ContainsFunc(problems, func(p config.Problem) bool {
		return p.Code == config.CodeParseError || p.Code == config.CodeUnsupportedFormat
	})
}

// fail returns the problems of a mock document as an error.
func fail(problems ...config.Problem) error {
	return &config.Error{Scope: "mock", Problems: problems}
}

// workingDirHint says where a relative path starts.
func workingDirHint(path string) string {
	if wd, err := os.Getwd(); err == nil && !filepath.IsAbs(path) {
		return "relative paths start at " + wd
	}

	return ""
}
