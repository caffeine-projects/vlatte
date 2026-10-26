package mockgrpc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/config"
)

var (
	_ vlatte.Builder[*Mock, *Server] = (*fileBuilder)(nil)
	_ vlatte.Builder[*Mock, *Server] = (*bytesBuilder)(nil)
)

// MockFileFormats lists the formats of mock files, as file extensions without the dot.
func MockFileFormats() []string {
	return config.Formats()
}

type fileBuilder struct {
	path string
}

// FromFile builds a mock from a JSON or YAML file, chosen by its extension: .json, .yaml or .yml.
//
//	id: greet-ann
//	request:
//	  method: greeter.Greeter/SayHello
//	  fields: {name: ann}
//	response:
//	  message: {message: hello ann}
//
// A relative body_file opens from the file's directory. When the file has problems, the error is a list of every one,
// each with its key and a hint.
func FromFile(path string) vlatte.Builder[*Mock, *Server] {
	return &fileBuilder{path: path}
}

// Build implements vlatte.Builder.
func (b *fileBuilder) Build(app *Server) (*Mock, error) {
	origin := "file " + b.path

	format, ok := config.Format(filepath.Ext(b.path))
	if !ok {
		return nil, docProblems(config.Problem{
			Code: config.CodeUnsupportedFormat, Origin: origin,
			Message: fmt.Sprintf("mock file %q is not JSON or YAML", b.path), Hint: "use a .json, .yaml or .yml file",
		})
	}

	data, err := os.ReadFile(b.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, docProblems(config.Problem{
			Code: config.CodeFileNotFound, Origin: origin, Message: fmt.Sprintf("mock file %q does not exist", b.path),
		})
	case err != nil:
		return nil, docProblems(config.Problem{Code: config.CodeReadError, Origin: origin, Message: err.Error()})
	}

	return mockDoc{origin: origin, source: b.path, format: format, dir: filepath.Dir(b.path), data: data}.build(app)
}

type bytesBuilder struct {
	data   []byte
	format string
}

// FromBytes builds a mock from a JSON or YAML document, as FromFile does. format is json, yaml or yml. A relative
// body_file opens from the working directory.
func FromBytes(data []byte, format string) vlatte.Builder[*Mock, *Server] {
	return &bytesBuilder{data: data, format: format}
}

// Build implements vlatte.Builder.
func (b *bytesBuilder) Build(app *Server) (*Mock, error) {
	return mockDoc{origin: "bytes", format: b.format, data: b.data}.build(app)
}

// buildAdminMock builds a mock the admin API was sent. Its origin tells it from mocks of files and code.
func buildAdminMock(app *Server, content []byte, format string) (*Mock, error) {
	return mockDoc{origin: "admin request", source: handler.Origin, format: format, data: content}.build(app)
}

// mockDoc is a mock document and where it came from.
type mockDoc struct {
	origin string // where problems point, such as "file mocks/greet.yaml"
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
		return nil, docProblems(problems...)
	}

	b := &Builder{enabled: true}
	def.apply(b)

	if doc.source != "" {
		b.SetSource(doc.source)
	}

	// The document describes the mock as it was written.
	if parsed, err := config.Parse(doc.data, doc.format); err == nil {
		b.doc, _ = parsed.(map[string]any)
	}

	mock, err := b.Build(app)
	if err != nil {
		return nil, docProblems(config.Problem{Code: config.CodeInvalidValue, Origin: doc.origin, Message: err.Error()})
	}

	return mock, nil
}

// parseFailed reports whether the document couldn't be read at all, so checks between its keys would only add noise.
func parseFailed(problems []config.Problem) bool {
	return slices.ContainsFunc(problems, func(p config.Problem) bool {
		return p.Code == config.CodeParseError || p.Code == config.CodeUnsupportedFormat
	})
}

// docProblems returns the problems of a mock document as an error.
func docProblems(problems ...config.Problem) error {
	return &config.Error{Scope: "mock", Problems: problems}
}
