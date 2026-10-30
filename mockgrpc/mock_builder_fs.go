package mockgrpc

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mockfile"
)

var _ vlatte.Builder[*Mock, *Server] = mockDoc{}

// MockFileFormats lists the formats of mock files, as file extensions without the dot.
func MockFileFormats() []string {
	return config.Formats()
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
// each with its key and a hint. A YAML stream must contain one nonempty mock document: use [ConfigBuilder.MockFiles]
// to load multiple documents from a file.
func FromFile(path string) vlatte.Builder[*Mock, *Server] {
	return mockfile.File(path, nil, "use MockFiles to load multiple mock documents", buildDocument)
}

// FromBytes builds a mock from a JSON or YAML document, as FromFile does. format is json, yaml or yml. A relative
// body_file opens from the working directory. Multiple nonempty YAML documents are rejected.
func FromBytes(data []byte, format string) vlatte.Builder[*Mock, *Server] {
	return (mockfile.Document{Origin: "bytes", Format: format, Data: data}).Builder("use MockFiles to load multiple mock documents", buildDocument)
}

// buildAdminMock builds a mock the admin API was sent. Its origin tells it from mocks of files and code.
func buildAdminMock(app *Server, content []byte, format string) (*Mock, error) {
	return mockDoc{Origin: "admin request", Source: adminapi.Origin, Format: format, Data: content}.build(app)
}

// mockDoc is a mock document and where it came from.
type mockDoc struct {
	mockfile.Document
}

// Build implements [vlatte.Builder] for one parsed document or a file's read or parse error.
func (doc mockDoc) Build(app *Server) (*Mock, error) {
	def := newMockDef(app.matchers())
	def.dir = doc.Dir

	if err := doc.Decode(_mockSchema, &def, def.check); err != nil {
		return nil, err
	}

	b := &Builder{enabled: true}
	def.apply(b)

	if doc.Source != "" {
		b.SetSource(doc.Source)
	}

	// The document describes the mock as it was written.
	if parsed, err := config.Parse(doc.Data, doc.Format); err == nil {
		b.doc, _ = parsed.(map[string]any)
	}

	mock, err := b.Build(app)
	if err != nil {
		return nil, docProblems(config.Problem{Code: config.CodeInvalidValue, Origin: doc.Origin, Message: err.Error()})
	}

	return mock, nil
}

// build turns the single document into a mock for app.
func (doc mockDoc) build(app *Server) (*Mock, error) {
	return doc.Document.Builder("use MockFiles to load multiple mock documents", buildDocument).Build(app)
}

func buildDocument(doc mockfile.Document, app *Server) (*Mock, error) {
	return mockDoc{Document: doc}.Build(app)
}

// docProblems returns the problems of a mock document as an error.
func docProblems(problems ...config.Problem) error {
	return &config.Error{Scope: "mock", Problems: problems}
}
