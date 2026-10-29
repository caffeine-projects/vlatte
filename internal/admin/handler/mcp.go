package handler

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"reflect"
	"runtime/debug"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
)

// _mcpDone is the text of a tool whose operation answers nothing, as its route answers 204.
const _mcpDone = "done"

// NewMCP returns the MCP server of ops: a tool per operation the mock server can do, named after it in snake_case,
// such as add_mock for [Operations.AddMock]. A tool's result is the operation's, in JSON, as its route answers it,
// and the error of a tool whose operation fails is the operation's problem document.
func NewMCP(ops *Operations) *mcp.Server {
	impl := &mcp.Implementation{Name: "vlatte", Title: "vlatte mock server", Version: moduleVersion()}

	s := mcp.NewServer(impl, &mcp.ServerOptions{
		Instructions: "Manage a vlatte mock server while it runs: list, add and change its mocks, and read the requests " +
			"it served. Mocks are mock files as JSON objects: mock_schema gives their JSON Schema, where the server has one.",
	})

	addHealthTool(s, ops)
	addListMocksTool(s, ops)
	addGetMockTool(s, ops)
	addUpdateMockTool(s, ops)
	addRemoveMockTool(s, ops)
	addRemoveAllMocksTool(s, ops)
	addResetTool(s, ops)

	if ops.HasMockSchema() {
		addMockSchemaTool(s, ops)
	}

	if ops.Builds() {
		addAddMockTool(s, ops)
		addValidateMockTool(s, ops)
		addReplaceMockTool(s, ops)
	}

	if ops.HasFiles() {
		addReloadMocksTool(s, ops)
		addSaveMocksTool(s, ops)
	}

	if ops.Records() {
		addStartRecordingTool(s, ops)
		addRecordingStatusTool(s, ops)
		addStopRecordingTool(s, ops)
	}

	if ops.HasScenarios() {
		addListScenariosTool(s, ops)
		addSetScenarioTool(s, ops)
		addResetScenariosTool(s, ops)
		addResetScenarioTool(s, ops)
	}

	if ops.HasKV() {
		addListKVTool(s, ops)
		addGetKVTool(s, ops)
		addSetKVTool(s, ops)
		addDeleteKVTool(s, ops)
	}

	if ops.ReloadsKV() {
		addReloadKVTool(s, ops)
	}

	if ops.HasJournal() {
		addListRequestsTool(s, ops)
		addQueryRequestsTool(s, ops)
		addVerifyRequestsTool(s, ops)
		addGetRequestTool(s, ops)
		addClearRequestsTool(s, ops)
	}

	return s
}

// MCPHandler serves s over Streamable HTTP: POST /mcp. It keeps no sessions, so a client needs none, and answers in
// JSON. A request through a local address must name a local host, against DNS rebinding.
func MCPHandler(s *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

// ServeMCP serves s over in and out, newline-delimited JSON-RPC, as a client that starts the server as a process
// talks to it, until in ends or ctx is done.
func ServeMCP(ctx context.Context, s *mcp.Server, in io.Reader, out io.Writer) error {
	return s.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
}

// nopWriteCloser is a writer that closes nothing: the caller owns it.
type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

// _readOnly marks the tools of operations that change nothing.
var _readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true}

// noInput is the input of a tool that takes none.
type noInput struct{}

// addTool adds tool to s, which runs run on its input. run returns the operation's result, nil for none, or its
// error, whose problem document is the tool's error.
func addTool[In any](s *mcp.Server, tool *mcp.Tool, run func(ctx context.Context, in In) (any, error)) {
	serveTool(s, tool, func(ctx context.Context, _ jsontext.Value, in In) (any, error) { return run(ctx, in) })
}

// addArgumentsTool is like addTool, for a tool whose arguments, of the schema of In, are the document its operation
// takes: run takes them as they came.
func addArgumentsTool[In any](s *mcp.Server, tool *mcp.Tool, run func(ctx context.Context, args jsontext.Value) (any, error)) {
	serveTool(s, tool, func(ctx context.Context, args jsontext.Value, _ In) (any, error) { return run(ctx, args) })
}

// serveTool adds tool to s, which runs run on its arguments, as they came and decoded as In.
func serveTool[In any](s *mcp.Server, tool *mcp.Tool, run func(ctx context.Context, args jsontext.Value, in In) (any, error)) {
	// The SDK infers the schema of an input this can't describe, and refuses what neither can.
	if schema, err := inputSchema[In](); err == nil {
		tool.InputSchema = schema
	}

	mcp.AddTool(s, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, jsontext.Value(req.Params.Arguments), in)
		if err != nil {
			return problemResult(ctx, err, tool.Name), nil, nil
		}

		if out == nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: _mcpDone}}}, nil, nil
		}

		body, ok := out.(jsontext.Value)
		if !ok {
			if body, err = encode(out, _formatJSON); err != nil {
				return problemResult(ctx, err, tool.Name), nil, nil
			}
		}

		return toolResult(body, false), nil, nil
	})
}

// problemResult is the result of the tool name that failed with err: its problem document. Internal errors are logged,
// with the logger of ctx: the agent can't fix them.
func problemResult(ctx context.Context, err error, name string) *mcp.CallToolResult {
	p := problemOf(err)
	if p.status >= http.StatusInternalServerError {
		zerolog.Ctx(ctx).Error().Str("code", p.code).Str("tool", name).Msg("admin: " + p.detail)
	}

	body, encodeErr := encode(p.document(name), _formatJSON)
	if encodeErr != nil {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}
	}

	return toolResult(body, true)
}

// toolResult is the result of a tool that returns body, a JSON document, or fails with it, a problem document.
func toolResult(body jsontext.Value, failed bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(body)}},
		StructuredContent: body,
		IsError:           failed,
	}
}

// _documentSchema is the schema of a tool's argument that the operation takes as it comes, such as a mock: an object,
// which the operation checks.
var _documentSchema = &jsonschema.Schema{Type: "object"}

// inputSchema is the input schema of a tool whose input is In: its jsontext.Value fields are documents.
func inputSchema[In any]() (*jsonschema.Schema, error) {
	return jsonschema.For[In](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[jsontext.Value](): _documentSchema},
	})
}

// moduleVersion is the version of vlatte the program was built with, as the main module or a dependency, or
// "(devel)".
func moduleVersion() string {
	const path = "github.com/caffeine-projects/vlatte"

	version := "(devel)"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, m := range append([]*debug.Module{&info.Main}, info.Deps...) {
			if m.Path == path {
				version = cmp.Or(m.Version, version)
			}
		}
	}

	return version
}
