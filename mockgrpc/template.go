package mockgrpc

import (
	"context"
	json "encoding/json/v2"

	"google.golang.org/grpc/metadata"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/params"
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

// Kit is the vlatte.Kit of gRPC mocks: the server's template engine, and the template data of the call.
type Kit struct {
	engine vlatte.TemplateEngine
	data   *TemplateData
}

// TemplateEngine implements vlatte.Kit.
func (k *Kit) TemplateEngine() vlatte.TemplateEngine {
	return k.engine
}

// Data implements vlatte.Kit: the call's *TemplateData.
func (k *Kit) Data() any {
	return k.data
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
	}
}
