package mockhttp

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mcpwire"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

var (
	_ ReplierFactory         = (*MCPResult)(nil)
	_ vlatte.SelfDescriptive = (*MCPResult)(nil)
	_ Replier                = (*mcpResultReplier)(nil)
)

// errMCPResultTemplate is why NewReplier fails for a JSON result or a JSON-RPC error that is a template.
var errMCPResultTemplate = errors.New("mcp result: only a text result can be a template: use MCPText or MCPToolError with toJSON")

// mcpResultKind tells the results of a tool mock apart.
type mcpResultKind int

const (
	_mcpText mcpResultKind = iota + 1
	_mcpJSON
	_mcpError
)

// MCPResult is the answer of a tool mock to tools/call: a result, the tool's own error, or a JSON-RPC error, in the
// JSON-RPC envelope with the id of the request. It is a ReplierFactory: give it to Builder.Reply of a mock made with
// MCP and MCPTool.
//
// Only a text result can be a template. A sequence or a random reply of MCPResults serves, but its description doesn't
// load back as a mock file, so the admin API can't save it. On a mock that isn't a tool mock, a request whose body
// isn't a JSON-RPC message fails.
type MCPResult struct {
	kind      mcpResultKind
	text      string // of MCPText and MCPToolError, and the JSON of MCPJSON
	isError   bool
	code      int64
	message   string
	templated bool
	data      any
	err       error // why the value of MCPJSON didn't encode; NewReplier returns it
}

// MCPText returns a result of one text content: text.
func MCPText(text string) *MCPResult {
	return &MCPResult{kind: _mcpText, text: text}
}

// MCPJSON returns a result of one text content: v, encoded as JSON now, with the keys of maps in order.
func MCPJSON(v any) *MCPResult {
	b, err := mcpwire.Marshal(v)

	return &MCPResult{kind: _mcpJSON, text: string(b), err: err}
}

// MCPToolError returns a result of one text content, text, that is the tool's own error: isError is true. A client
// gets it as the tool's answer, not as an error of the call.
func MCPToolError(text string) *MCPResult {
	return &MCPResult{kind: _mcpText, text: text, isError: true}
}

// MCPError returns a JSON-RPC error with code and message, in place of a result: a client gets it as an error of the
// call.
func MCPError(code int64, message string) *MCPResult {
	return &MCPResult{kind: _mcpError, code: code, message: message}
}

// Template makes the text of the result a template, rendered with the data of a reply's templates, such as
// {{ .Request.Header.Get "X-Id" }}. The arguments of the call are
// {{ (fromJSON .Request.Body.String).params.arguments.<name> }}. Only a result of MCPText or MCPToolError can be one.
func (r *MCPResult) Template() *MCPResult {
	r.templated = true
	return r
}

// SetTemplateData sets data that the template reads as .Extra, as [Reply.SetTemplateData] does. The result keeps data,
// without copying it.
func (r *MCPResult) SetTemplateData(data any) *MCPResult {
	r.data = data
	return r
}

// Describe implements vlatte.SelfDescriptive: the result as the mcp_result key of a mock file.
func (r *MCPResult) Describe() vlatte.Descriptor {
	props := make(map[string]any)

	switch r.kind {
	case _mcpJSON:
		// As JSON, so its numbers keep their value.
		props["json"] = jsontext.Value(r.text)
	case _mcpError:
		props["error"] = map[string]any{"code": r.code, "message": r.message}
	default:
		props["text"] = r.text
		if r.isError {
			props["is_error"] = true
		}
	}

	if r.templated {
		tmpl := map[string]any{"enabled": true}
		if r.data != nil {
			tmpl["data"] = r.data
		}

		props["template"] = tmpl
	}

	return vlatte.NewDescriptor("mcp_result", props)
}

// NewReplier implements ReplierFactory.
func (r *MCPResult) NewReplier(app *Server) (Replier, error) {
	if r.err != nil {
		return nil, fmt.Errorf("mcp result: encoding the JSON: %w", r.err)
	}

	replier := &mcpResultReplier{result: *r}

	if !r.templated {
		return replier, nil
	}

	if r.kind != _mcpText {
		return nil, errMCPResultTemplate
	}

	tmpl, err := app.config.TemplateEngine.Compile("mcp_result.text", r.text)
	if err != nil {
		return nil, fmt.Errorf("mcp result: compiling the text: %w", err)
	}

	replier.tmpl = tmpl

	return replier, nil
}

// mcpResultReplier answers the tool calls of one mock with its result.
type mcpResultReplier struct {
	result MCPResult
	tmpl   vlatte.Template // the text as a template, when it is one
}

// Reply implements Replier.
func (r *mcpResultReplier) Reply(_ context.Context, in ReplyInput) (*Response, error) {
	m, err := mcpMessageOf(in.RequestValues)
	if err != nil {
		return nil, fmt.Errorf("mcp result: decoding the call: %w", err)
	}

	var answer mcpwire.Response

	switch r.result.kind {
	case _mcpError:
		answer = mcpwire.Fail(m.ID, r.result.code, r.result.message, nil)
	default:
		text := r.result.text

		if r.tmpl != nil {
			data := in.RequestValues.Data
			if d, ok := data.(*viewdata.Data); ok {
				data = d.WithExtra(r.result.data)
			}

			b, err := execute(r.tmpl, data)
			if err != nil {
				return nil, fmt.Errorf("mcp result: rendering the text: %w", err)
			}

			text = string(b)
		}

		answer = mcpwire.Result(m.ID, mcpwire.CallToolResult{
			Content: []mcpwire.Content{{Type: "text", Text: text}},
			IsError: r.result.isError,
		})
	}

	return mcpResponse(http.StatusOK, answer)
}

// mcpResponse returns the response with status that holds answer, as JSON.
func mcpResponse(status int, answer mcpwire.Response) (*Response, error) {
	body, err := mcpwire.MarshalResponse(answer)
	if err != nil {
		return nil, fmt.Errorf("mcp: encoding the answer: %w", err)
	}

	res := newResponse()
	res.StatusCode = status
	res.Header.Set(HeaderContentType, MIMEApplicationJSON)
	res.Body = body

	return res, nil
}
