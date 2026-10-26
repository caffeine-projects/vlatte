package handler

import (
	"context"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"runtime/debug"

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
	}

	if ops.HasJournal() {
		addListRequestsTool(s, ops)
		addQueryRequestsTool(s, ops)
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
	mcp.AddTool(s, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := run(ctx, in)
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

// moduleVersion is the version of vlatte the program was built with, or "(devel)".
func moduleVersion() string {
	const path = "github.com/caffeine-projects/vlatte"

	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Path == path && info.Main.Version != "" {
			return info.Main.Version
		}

		for _, dep := range info.Deps {
			if dep.Path == path {
				return dep.Version
			}
		}
	}

	return "(devel)"
}

// document encodes v, an object of a tool's input, as the JSON document the operation takes.
func document(v any) ([]byte, error) {
	return encode(v, _formatJSON)
}
