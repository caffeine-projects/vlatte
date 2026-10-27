package mockhttp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var (
	_ vlatte.Builder[*Mock, *Server] = (*fileBuilder)(nil)
	_ vlatte.Builder[*Mock, *Server] = (*bytesBuilder)(nil)
	_ vlatte.Builder[*Mock, *Server] = mockDoc{}
)

// MockFileFormats lists the formats of mock files and recordings, as file extensions without the dot.
func MockFileFormats() []string {
	return config.Formats()
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
// A YAML file of several mocks, one per document, fails: the server loads those from its mock file patterns.
func FromFile(path string) vlatte.Builder[*Mock, *Server] {
	return &fileBuilder{path: path}
}

func (b *fileBuilder) Build(app *Server) (*Mock, error) {
	doc, err := readMockFile(b.path)
	if err != nil {
		return nil, err
	}

	return doc.build(app)
}

// readMockFile reads the mock file at path. Every failure is a *config.Error listing each problem.
func readMockFile(path string) (mockDoc, error) {
	origin := "file " + path

	format, ok := mockFileFormat(filepath.Ext(path))
	if !ok {
		return mockDoc{}, fail(config.Problem{
			Code: config.CodeUnsupportedFormat, Origin: origin,
			Message: fmt.Sprintf("mock file %q is not JSON or YAML", path), Hint: "use a .json, .yaml or .yml file",
		})
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return mockDoc{}, fail(config.Problem{
			Code: config.CodeFileNotFound, Origin: origin,
			Message: fmt.Sprintf("mock file %q does not exist", path), Hint: workingDirHint(path),
		})
	case err != nil:
		return mockDoc{}, fail(config.Problem{Code: config.CodeReadError, Origin: origin, Message: err.Error()})
	}

	return mockDoc{origin: origin, source: path, format: format, dir: filepath.Dir(path), data: data}, nil
}

// fileMocks returns a builder for each mock of the mock file at path, in the order the file holds them: a YAML file
// holds one per document. A file that can't be read gives one builder, which fails as FromFile does.
func fileMocks(path string) []vlatte.Builder[*Mock, *Server] {
	doc, err := readMockFile(path)
	if err != nil {
		return []vlatte.Builder[*Mock, *Server]{FromFile(path)}
	}

	docs := config.Documents(doc.data, doc.format)
	if len(docs) == 1 {
		doc.data = docs[0]
		return []vlatte.Builder[*Mock, *Server]{doc}
	}

	builders := make([]vlatte.Builder[*Mock, *Server], 0, len(docs))
	for i, data := range docs {
		mock := doc
		mock.origin = fmt.Sprintf("%s, mock %d", doc.origin, i+1)
		mock.data = data
		builders = append(builders, mock)
	}

	return builders
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

// buildAdminMock builds a mock the admin API was sent. Its relative body files open from the working directory, as
// the admin API describes the body files of mocks. Its origin tells it from mocks of files and code.
func buildAdminMock(app *Server, content []byte, format string) (*Mock, error) {
	return mockDoc{origin: "admin request", source: adminapi.Origin, format: format, data: content}.build(app)
}

// mockDoc is a mock definition document and where it came from.
type mockDoc struct {
	origin string // where problems point, such as "file mocks/users.yaml"
	source string // the mock's source, a file path; empty for other documents
	format string // json, yaml or yml
	dir    string // where a relative body_file opens from; empty is the working directory
	data   []byte
}

// Build implements vlatte.Builder, as build does.
func (doc mockDoc) Build(app *Server) (*Mock, error) {
	return doc.build(app)
}

// build turns the document into a mock for app. Every failure is a *config.Error listing each problem, as is a YAML
// document of several mocks.
func (doc mockDoc) build(app *Server) (*Mock, error) {
	// Parsed whole, a stream would give its first document, which can be an empty one before the mock.
	docs := config.Documents(doc.data, doc.format)
	if len(docs) > 1 {
		return nil, fail(severalMocks(doc.origin, len(docs)))
	}

	doc.data = docs[0]

	def := newMockDef(app.matchers())
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

// severalMocks is the problem of a document of n mocks, where one is expected.
func severalMocks(origin string, n int) config.Problem {
	return config.Problem{
		Code: config.CodeInvalidValue, Origin: origin,
		Message: fmt.Sprintf("the document holds %d mocks, where one is expected", n),
		Hint:    "build each mock on its own; only the mock files a server loads from its patterns hold several",
	}
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
