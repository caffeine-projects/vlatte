package mockhttp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mockfile"
)

var _ vlatte.Builder[*Mock, *Server] = mockDoc{}

// MockFileFormats lists the formats of mock files and recordings, as file extensions without the dot.
func MockFileFormats() []string {
	return config.Formats()
}

// mockFileFormat normalizes a format or an extension, such as ".YAML", and reports whether mock files can use it.
func mockFileFormat(format string) (string, bool) {
	return config.Format(format)
}

// FromFile builds a mock from a JSON or YAML file, chosen by its extension: .json, .yaml or .yml.
//
// The file is data: only the settings that are templates, such as a templated reply body, are compiled, with the
// template engine of the server. Templates read the server's key-value store, data and URL when a request arrives.
// A relative body_file opens from the file's directory.
// When the file has problems, the error is a list of every one, each with its key and a hint.
// A YAML file must contain one mock; use [ConfigBuilder.MockFilePatterns] to load multiple mock documents.
func FromFile(path string) vlatte.Builder[*Mock, *Server] {
	return mockfile.File(path, workingDirHint, "use MockFilePatterns to load multiple mock documents", buildDocument)
}

// FromBytes builds a mock from a JSON or YAML document. format is json, yaml or yml.
// Like a mock file, the document is data; see FromFile.
// A relative body_file opens from the working directory.
// Multiple YAML mock documents are rejected.
func FromBytes(data []byte, format string) vlatte.Builder[*Mock, *Server] {
	return (mockfile.Document{Origin: "bytes", Format: format, Data: data}).Builder("use MockFilePatterns to load multiple mock documents", buildDocument)
}

// buildAdminMock builds a mock the admin API was sent. Its relative body files open from the working directory, as
// the admin API describes the body files of mocks. Its origin tells it from mocks of files and code.
func buildAdminMock(app *Server, content []byte, format string) (*Mock, error) {
	return mockDoc{Origin: "admin request", Source: adminapi.Origin, Format: format, Data: content}.build(app)
}

// mockDoc is a mock definition document and where it came from.
type mockDoc struct {
	mockfile.Document
}

// Build turns one expanded document into a mock for app.
func (doc mockDoc) Build(app *Server) (*Mock, error) {
	def := newMockDef(app.matchers())
	def.dir = doc.Dir

	if err := doc.Decode(_mockSchema, &def, def.check); err != nil {
		return nil, err
	}

	b := Request()
	def.apply(b, doc.Dir)

	if doc.Source != "" {
		b.SetSource(doc.Source)
	}

	var problems []config.Problem
	if def.ext != nil {
		for i, handler := range app.config.MockFileHandlers {
			if err := handler.Handle(def.ext, b); err != nil {
				problems = append(problems, config.Problem{
					Code: config.CodeInvalidValue, Origin: doc.Origin, Key: "ext",
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
		return nil, fail(config.Problem{Code: config.CodeInvalidValue, Origin: doc.Origin, Message: err.Error()})
	}

	return mock, nil
}

// build turns the single document into a mock for app.
func (doc mockDoc) build(app *Server) (*Mock, error) {
	return doc.Document.Builder("use MockFilePatterns to load multiple mock documents", buildDocument).Build(app)
}

func buildDocument(doc mockfile.Document, app *Server) (*Mock, error) {
	return mockDoc{Document: doc}.Build(app)
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
