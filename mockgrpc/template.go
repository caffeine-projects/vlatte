package mockgrpc

import (
	"context"
	json "encoding/json/v2"
	"maps"
	"net/http"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/params"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

var _ vlatte.Kit = (*Kit)(nil)

// Parameters gives templates the server's parameters as text: {{ .Parameters.Get "key" }}. A parameter that may be
// missing reads with Has first: {{ if .Parameters.Has "key" }}.
type Parameters = params.Parameters

// TemplateData is what templates render with: the dot of the templates of [Template] replies, and the data of the
// [Kit] of event listeners, such as webhooks.
type TemplateData struct {
	// Request is the call being served.
	Request *TemplateRequest

	// Parameters reads the server's parameters: {{ .Parameters.Get "key" }}.
	Parameters *Parameters

	// Scenarios reads the state of the server's scenarios: {{ .Scenarios.State "checkout" }}. A scenario the mock that
	// matched moved is in the state it moved it to, even once another request moved it on.
	Scenarios matchscenario.View

	// Data is the data set with Server.SetData, in force when the call arrived: {{ .Data.target }}.
	Data map[string]any

	// Provided is what the server's template data providers gave for the call, by their key: {{ .Provided.user.name }}.
	Provided map[string]any
}

// TemplateDataProvider adds data to the templates of gRPC mocks, which read it as .Provided.<Key>. The server calls
// Data once for each call a mock matched, before the mock waits and replies: an error fails the call, with the
// vlatte-error trailer.
type TemplateDataProvider = vlatte.TemplateDataProvider[*TemplateDataProviderInput]

// TemplateDataProviderInput is what a TemplateDataProvider receives for each call.
type TemplateDataProviderInput struct {
	call *Call
	mock *Mock
}

// Call returns the call being served.
func (in *TemplateDataProviderInput) Call() *Call {
	return in.call
}

// Mock returns the mock that matched the call.
func (in *TemplateDataProviderInput) Mock() *Mock {
	return in.mock
}

// TemplateRequest is the call being served, to templates.
type TemplateRequest struct {
	// Method is the full method of the call: /pkg.Service/Method.
	Method string

	// Header is the metadata the client sent: {{ index .Request.Header "x-team" 0 }}.
	Header metadata.MD

	// Message is the request message as JSON reads it, field by field, with the field names of the .proto file:
	// {{ .Request.Message.user_id }}. A client streaming call has the array of its messages: {{ index
	// .Request.Message 0 }}.
	Message any
}

// Kit is the vlatte.Kit of gRPC mocks: the server's template engine, logger and webhook transport, the template data
// of the call, and the directory of the mock file of the event it serves.
type Kit struct {
	engine    vlatte.TemplateEngine
	data      *TemplateData
	logger    *zerolog.Logger
	transport http.RoundTripper
	dir       string
}

// TemplateEngine implements vlatte.Kit.
func (k *Kit) TemplateEngine() vlatte.TemplateEngine {
	return k.engine
}

// Data implements vlatte.Kit: the call's *TemplateData.
func (k *Kit) Data() any {
	return k.data
}

// Logger implements vlatte.Kit: the server's logger.
func (k *Kit) Logger() *zerolog.Logger {
	return k.logger
}

// Transport implements vlatte.Kit: a transport the server makes and closes with it.
func (k *Kit) Transport() http.RoundTripper {
	return k.transport
}

// Dir implements vlatte.Kit: the directory of the mock file that defines the event, or "" for the working directory,
// as for events defined in Go and global events.
func (k *Kit) Dir() string {
	return k.dir
}

// SetData sets the data templates read as .Data, such as the address of another server: {{ .Data.target }}. The
// server keeps a copy of the map, and calls read the data in force when they arrive.
func (app *Server) SetData(data map[string]any) {
	data = maps.Clone(data)
	app.data.Store(&data)
}

// Data returns the data templates read as .Data. Calls read the map, so don't change it: set new data instead.
func (app *Server) Data() map[string]any {
	if data := app.data.Load(); data != nil {
		return *data
	}

	return nil
}

// provide gives call the data of the server's template data providers, and the server's data, as the call arrives:
// mock matched it. It returns the error of the first provider that fails.
func (app *Server) provide(call *Call, mock *Mock) error {
	provided, err := vlatte.ProvideTemplateData(app.config.TemplateDataProviders, &TemplateDataProviderInput{call: call, mock: mock})
	call.data, call.provided = app.Data(), provided

	return err
}

// templateEngine returns the engine of the server's templates.
func (app *Server) templateEngine() vlatte.TemplateEngine {
	return app.config.TemplateEngine
}

// templateData returns the template data of call, whose parameters read with ctx.
func (app *Server) templateData(ctx context.Context, call *Call) *TemplateData {
	// The request is as protojson writes it: JSON, which reads.
	var msg any
	if b := call.JSON(); len(b) > 0 {
		_ = json.Unmarshal(b, &msg)
	}

	return &TemplateData{
		Request: &TemplateRequest{
			Method:  call.method,
			Header:  call.header,
			Message: msg,
		},
		Parameters: params.New(ctx, app.config.Parameters),
		Scenarios:  app.scenarios.View(call.scenarioMoves...),
		Data:       call.data,
		Provided:   call.provided,
	}
}
