package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mcpwire"
)

var (
	_ ReplierFactory = (*mcpEndpointReply)(nil)
	_ Replier        = (*mcpEndpointReplier)(nil)
)

// mcpEndpointReply is the reply of the endpoint mock of an MCP server: MCPServerInfo sets it. The mcp key of the mock
// describes it.
type mcpEndpointReply struct {
	endpoint string
	server   mcpwire.Implementation
}

// NewReplier implements ReplierFactory.
func (r *mcpEndpointReply) NewReplier(app *Server) (Replier, error) {
	return &mcpEndpointReplier{mcpEndpointReply: *r, app: app}, nil
}

// mcpEndpointReplier answers what an MCP server answers on its own: the lifecycle, ping and tools/list.
type mcpEndpointReplier struct {
	mcpEndpointReply

	app *Server
}

// Reply implements Replier.
func (r *mcpEndpointReplier) Reply(ctx context.Context, in ReplyInput) (*Response, error) {
	if in.RequestValues.RawRequest.Method != http.MethodPost {
		res := newResponse()
		res.StatusCode = http.StatusMethodNotAllowed
		res.Header.Set(HeaderAllow, http.MethodPost)

		return res, nil
	}

	m, err := mcpMessageOf(in.RequestValues)

	switch {
	case errors.Is(err, mcpwire.ErrParse):
		return mcpResponse(http.StatusBadRequest,
			mcpwire.Fail(nil, mcpwire.CodeParseError, "parse error: the body is not JSON", nil))
	case err != nil:
		return mcpResponse(http.StatusBadRequest,
			mcpwire.Fail(nil, mcpwire.CodeInvalidRequest, "invalid request: expected one JSON-RPC 2.0 object", nil))
	case !m.IsRequest():
		// A notification, or a response of the client, takes no answer.
		res := newResponse()
		res.StatusCode = http.StatusAccepted

		return res, nil
	}

	switch m.Method {
	case "initialize":
		version := mcpwire.AgreeVersion(m.InitializeVersion())

		capabilities := make(map[string]any)
		capabilities["tools"] = make(map[string]any)

		return mcpResponse(http.StatusOK, mcpwire.Result(m.ID, mcpwire.InitializeResult{
			ProtocolVersion: version,
			Capabilities:    capabilities,
			ServerInfo:      r.server,
		}))
	case "ping":
		return mcpResponse(http.StatusOK, mcpwire.Result(m.ID, make(map[string]any)))
	case "tools/list":
		tools, err := r.tools(ctx)
		if err != nil {
			return nil, err
		}

		return mcpResponse(http.StatusOK, mcpwire.Result(m.ID, mcpwire.ListToolsResult{Tools: tools}))
	default:
		return mcpResponse(http.StatusOK, mcpwire.Fail(m.ID, mcpwire.CodeMethodNotFound, "method not found", nil))
	}
}

// tools lists the tools of the enabled tool mocks of the endpoint, in the order they match: one per name, as the first
// mock of the name describes it.
func (r *mcpEndpointReplier) tools(ctx context.Context) ([]mcpwire.Tool, error) {
	mocks, err := r.app.storage.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp: listing the tools: %w", err)
	}

	tools := make([]mcpwire.Tool, 0)
	seen := make(map[string]struct{})

	for m := range mocks {
		s := m.spec.mcp
		if s == nil || s.tool == nil || s.endpoint != r.endpoint {
			continue
		}

		if _, ok := seen[s.tool.name]; ok {
			continue
		}

		seen[s.tool.name] = struct{}{}

		schema := s.tool.inputSchema
		if len(schema) == 0 {
			schema = _mcpDefaultInputSchema
		}

		tools = append(tools, mcpwire.Tool{Name: s.tool.name, Description: s.tool.description, InputSchema: schema})
	}

	return tools, nil
}

// hasMCPEndpoint reports whether mocks hold an enabled endpoint mock of an MCP server at path.
func hasMCPEndpoint(mocks iter.Seq[*Mock], path string) bool {
	for m := range mocks {
		if endpoint, ok := isMCPEndpoint(m); ok && endpoint == path {
			return true
		}
	}

	return false
}

// answerMCPToolCall answers nm, a tool call no tool mock matched, on the path of an enabled endpoint mock of stored,
// with a JSON-RPC error whose data lists the near misses, and reports true. It answers nothing, and reports false, for
// any other request.
func (h *mockHandler) answerMCPToolCall(w http.ResponseWriter, nm *NoMatch, stored iter.Seq[*Mock]) bool {
	r := nm.Request
	if r.RawRequest.Method != http.MethodPost {
		return false
	}

	// The path first: on a server without an endpoint mock there, no body is decoded.
	if !hasMCPEndpoint(stored, r.URL.Path) {
		return false
	}

	m, err := mcpMessageOf(r)
	if err != nil || !m.IsToolCall() {
		return false
	}

	// An empty list, not null, when there are none, as when the server reports no near misses.
	misses := nm.NearMisses
	if misses == nil {
		misses = make([]vlatte.NearMiss, 0)
	}

	body, err := mcpwire.Marshal(mcpwire.Fail(m.ID, mcpwire.CodeInvalidParams,
		fmt.Sprintf("mcp: no mock of tool %q matched the call", m.ToolName()),
		map[string]any{"near_misses": misses}))
	if err != nil {
		return false
	}

	w.Header().Set(HeaderContentType, MIMEApplicationJSON)
	w.WriteHeader(h.app.config.MCPRequestWasNotMatchedStatusCode)
	_, _ = w.Write(body)

	return true
}
