package mockhttp

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// startMCPEndpoints starts a server with two MCP servers: a at /mcp, with tools, and b at /other, with one.
func startMCPEndpoints(t *testing.T) *Server {
	t.Helper()

	return startMCP(t, nil,
		MCP("/mcp").MCPServerInfo("a", "1.0.0"),
		MCP("/mcp").MCPTool("t", "first").MCPInputSchema(map[string]any{"type": "object", "required": []string{"q"}}).
			Reply(MCPText("1")),
		MCP("/mcp").MCPTool("t", "second").MCPArgument("q", match.Equal("x")).Reply(MCPText("2")),
		MCP("/mcp").MCPTool("u", "").Reply(MCPText("u")),
		MCP("/mcp").MCPTool("off", "").Enabled(false).Reply(MCPText("off")),
		MCP("/other").MCPServerInfo("b", "2.0.0"),
		MCP("/other").MCPTool("v", "").Reply(MCPText("v")),
	)
}

func TestMCPEndpoint_Initialize(t *testing.T) {
	// The endpoint agrees to a version it knows, and answers its latest to one it doesn't, which a client then checks.
	m := startMCPEndpoints(t)

	tests := []struct {
		name   string
		params string
		want   string
	}{
		{name: "a version it knows", params: `{"protocolVersion":"2025-06-18"}`, want: "2025-06-18"},
		{name: "a newer one", params: `{"protocolVersion":"2026-07-28"}`, want: "2025-11-25"},
		{name: "none", params: `{}`, want: "2025-11-25"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := mcpPost(t, m.URL("/mcp"), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":`+tt.params+`}`)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"`+tt.want+`",`+
				`"capabilities":{"tools":{}},"serverInfo":{"name":"a","version":"1.0.0"}}}`, body)
		})
	}
}

func TestMCPEndpoint_PingAnswersEmptyResult(t *testing.T) {
	m := startMCPEndpoints(t)

	res, body := mcpPost(t, m.URL("/mcp"), `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{}}`, body)
}

func TestMCPEndpoint_NotificationsAndResponsesGet202(t *testing.T) {
	m := startMCPEndpoints(t)

	for _, msg := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":5,"result":{}}`,
	} {
		res, body := mcpPost(t, m.URL("/mcp"), msg)
		require.Equal(t, http.StatusAccepted, res.StatusCode)
		require.Empty(t, body)
	}
}

func TestMCPEndpoint_OtherHTTPMethodsGet405(t *testing.T) {
	// A client listens on a GET of the endpoint for what the server sends on its own: there is nothing, so it stops.
	m := startMCPEndpoints(t)

	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		res, body := do(t, method, m.URL("/mcp"), nil, nil)
		require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode, method)
		require.Equal(t, http.MethodPost, res.Header.Get(HeaderAllow), method)
		require.Empty(t, body, method)
	}
}

func TestMCPEndpoint_UnknownMethodGetsMethodNotFound(t *testing.T) {
	// The Go SDK client asks for server/discover first, and falls back to initialize on this error.
	m := startMCPEndpoints(t)

	res, body := mcpPost(t, m.URL("/mcp"), `{"jsonrpc":"2.0","id":"d","method":"server/discover"}`)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":"d","error":{"code":-32601,"message":"method not found"}}`, body)
}

func TestMCPEndpoint_BadBodiesGet400(t *testing.T) {
	m := startMCPEndpoints(t)

	res, body := mcpPost(t, m.URL("/mcp"), `not json`)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error: the body is not JSON"}}`,
		body)

	res, body = mcpPost(t, m.URL("/mcp"), `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,`+
		`"message":"invalid request: expected one JSON-RPC 2.0 object"}}`, body)
}

func TestMCPEndpoint_ToolsList(t *testing.T) {
	// Each name once, as its first mock describes it; disabled mocks and the tools of another endpoint left out.
	m := startMCPEndpoints(t)

	res, body := mcpPost(t, m.URL("/mcp"), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{"tools":[`+
		`{"name":"t","description":"first","inputSchema":{"required":["q"],"type":"object"}},`+
		`{"name":"u","inputSchema":{"type":"object"}}]}}`, body)

	res, body = mcpPost(t, m.URL("/other"), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"v","inputSchema":{"type":"object"}}]}}`, body)
}

func TestMCPEndpoint_UnmatchedToolCallAnswersJSONRPCError(t *testing.T) {
	// An MCP client fails its whole session on an error answer whose body is no JSON-RPC message, so a tool call no
	// tool mock matches gets one, with the mocks that came closest.
	m := startMCP(t, nil,
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").ID("get-user").MCPTool("get_user", "").MCPArgument("id", match.Equal(1)).Reply(MCPText("ada")),
	)

	// 200: a client that takes another status as a failure of the transport would drop the error.
	res, body := mcpPost(t, m.URL("/mcp"), toolCall("get_user", `{"id":2}`))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(HeaderVlatteError))
	require.Equal(t, MIMEApplicationJSON, res.Header.Get(HeaderContentType))

	var answer struct {
		ID    int `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				NearMisses []vlatte.NearMiss `json:"near_misses"`
			} `json:"data"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal([]byte(body), &answer))
	require.Equal(t, 1, answer.ID)
	require.Equal(t, -32602, answer.Error.Code)
	require.Equal(t, `mcp: no mock of tool "get_user" matched the call`, answer.Error.Message)
	require.Equal(t, []vlatte.NearMiss{{
		MockID: "get-user",
		Passed: 3,
		Total:  4,
		Failed: []string{"mcp arguments.id"},
	}}, answer.Error.Data.NearMisses)
}

func TestMCPEndpoint_UnmatchedToolCallWithoutEndpointIsOrdinaryNoMatch(t *testing.T) {
	// Without an endpoint mock on the path, nothing says the path is an MCP server's: the server answers as for any
	// request no mock matches.
	m := startMCP(t, nil, MCP("/mcp").MCPTool("get_user", "").Reply(MCPText("ada")))

	res, body := mcpPost(t, m.URL("/mcp"), toolCall("other", `{}`))
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.NotContains(t, body, `"jsonrpc"`)
}

func TestMCPEndpoint_ProxyAllTakesUnmatchedToolCall(t *testing.T) {
	// proxy_all forwards what no mock matches, tool calls on an MCP endpoint's path included.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("from target"))
	}))
	t.Cleanup(target.Close)

	m := startMCP(t, nil,
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").MCPTool("get_user", "").Reply(MCPText("ada")),
		ProxyAll(From(target.URL)),
	)

	res, body := mcpPost(t, m.URL("/mcp"), toolCall("other", `{}`))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "from target", body)
}

func TestMCPEndpoint_UnmatchedToolCallKeepsThreeNearMisses(t *testing.T) {
	// The endpoint mock, which no tool call matches, takes no place among the three near misses: the REST mock that
	// ties with it in the ranking keeps its place.
	m := startMCP(t, nil,
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").ID("a").MCPTool("t", "").MCPArgument("x", match.Equal(1)).Reply(MCPText("a")),
		MCP("/mcp").ID("b").MCPTool("t", "").MCPArgument("x", match.Equal(2)).Reply(MCPText("b")),
		Postf("/x").ID("rest").Reply(OK()),
	)

	_, body := mcpPost(t, m.URL("/mcp"), toolCall("t", `{"x":4}`))

	var answer struct {
		Error struct {
			Data struct {
				NearMisses []vlatte.NearMiss `json:"near_misses"`
			} `json:"data"`
		} `json:"error"`
	}

	require.NoError(t, json.Unmarshal([]byte(body), &answer))

	ids := make([]string, 0, len(answer.Error.Data.NearMisses))
	for _, n := range answer.Error.Data.NearMisses {
		ids = append(ids, n.MockID)
	}

	require.Equal(t, []string{"a", "b", "rest"}, ids)
}

func TestMCPEndpoint_UnmatchedToolCallStatusIsConfigurable(t *testing.T) {
	m := startMCP(t, []Option{Setup().MCPNotFoundStatusCode(http.StatusNotFound)},
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").MCPTool("get_user", "").Reply(MCPText("ada")),
	)

	res, body := mcpPost(t, m.URL("/mcp"), toolCall("other", `{}`))
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Contains(t, body, `"code":-32602`)

	// A REST request no mock matches keeps the server's no-match status.
	res, _ = do(t, http.MethodGet, m.URL("/users"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}
