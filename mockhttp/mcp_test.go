package mockhttp

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// mcpPost sends body, a JSON-RPC message, to url as an MCP client does, and returns the answer and its body.
func mcpPost(t *testing.T, url, body string) (*http.Response, string) {
	t.Helper()

	res, b := do(t, http.MethodPost, url, http.Header{HeaderContentType: {MIMEApplicationJSON}}, []byte(body))

	return res, string(b)
}

// toolCall returns a tools/call of the tool name with the arguments args, a JSON object, and the id 1.
func toolCall(name, args string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// startMCP starts a server with mocks and closes it when the test ends.
func startMCP(t *testing.T, opts []Option, mocks ...vlatte.Builder[*Mock, *Server]) *Server {
	t.Helper()

	m := MustNew(append([]Option{Setup().Silent()}, opts...)...)
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mocks...)

	return m
}

func TestMCP_ContradictoryMocksFailToBuildWithTheFix(t *testing.T) {
	// An MCP mock matches its own method and path, and is the endpoint or a tool: what contradicts that fails to build,
	// saying how to fix it.
	tests := []struct {
		name string
		mock *Builder
		want error
	}{
		{name: "neither endpoint nor tool", mock: MCP("/mcp"), want: errMCPMockKind},
		{name: "a method", mock: MCP("/mcp").MCPTool("t", "").Method(http.MethodGet), want: errMCPRequestLine},
		{name: "a path", mock: MCP("/mcp").MCPTool("t", "").URLPath(match.Equal("/x")), want: errMCPRequestLine},
		{name: "a URL", mock: MCP("/mcp").MCPTool("t", "").URL(match.Equal("http://x/mcp")), want: errMCPRequestLine},
		{
			name: "a path template",
			mock: MCP("/mcp").MCPTool("t", "").URLPathTemplate("/x/{id}").PathParam("id", match.Equal("1")),
			want: errMCPRequestLine,
		},
		{name: "a reply on the endpoint", mock: MCP("/mcp").MCPServerInfo("s", "1").Reply(OK()), want: errMCPEndpointReply},
		{name: "a tool without MCP", mock: Request().MCPTool("t", ""), want: errMCPNotStarted},
		{name: "endpoint and tool", mock: MCP("/mcp").MCPServerInfo("s", "1").MCPTool("t", ""), want: errMCPKindSet},
		{name: "an argument before the tool", mock: MCP("/mcp").MCPArgument("a", match.Equal(1)), want: errMCPNoTool},
		{name: "a server after a tool", mock: MCP("/mcp").MCPTool("t", "").MCPServerInfo("s", "1"), want: errMCPKindSet},
		{
			name: "an input schema before the tool",
			mock: MCP("/mcp").MCPInputSchema(map[string]any{"type": "object"}),
			want: errMCPNoTool,
		},
		{name: "an argument without MCP", mock: Request().MCPArgument("a", match.Equal(1)), want: errMCPNotStarted},
		{
			name: "a path param",
			mock: MCP("/mcp").MCPTool("t", "").PathParam("id", match.Equal("1")).Reply(MCPText("x")),
			want: errMCPRequestLine,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := MustNew(Setup().Silent())

			_, err := m.Mock(tt.mock)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestMCP_BuildFailsWithNoBodyParsing(t *testing.T) {
	// MCP mocks read the JSON-RPC message of the body, which a server that parses no bodies doesn't keep.
	m := MustNew(Setup().Silent().NoBodyParsing())

	_, err := m.Mock(MCP("/mcp").MCPServerInfo("s", "1"))
	require.ErrorIs(t, err, errMCPNoBody)
}

func TestMCP_BuildFailsForBadEndpointOrSchema(t *testing.T) {
	m := MustNew(Setup().Silent())

	_, err := MCP("mcp").MCPServerInfo("s", "1").Build(m)
	require.EqualError(t, err, `mock: mcp: endpoint "mcp": expected a path that starts with /`)

	_, err = MCP("/mcp").MCPTool("t", "").Method(http.MethodGet).Reply(MCPText("x")).Build(m)
	require.EqualError(t, err, "mock: mcp: an MCP mock matches its own method and path: remove Method")

	_, err = MCP("/mcp").MCPTool("t", "").MCPInputSchema("x").Reply(MCPText("x")).Build(m)
	require.EqualError(t, err, `mock: mcp: input schema: expected a JSON object, not "x"`)

	_, err = MCP("/mcp").MCPTool("t", "").MCPInputSchema(make(chan int)).Reply(MCPText("x")).Build(m)
	require.ErrorContains(t, err, "mock: mcp: input schema: json: ")

	_, ok := errors.AsType[*json.SemanticError](err)
	require.True(t, ok, "%v is no JSON encoding error", err)

	// A schema is what the user wrote: one that isn't UTF-8 fails, rather than show something else in tools/list.
	_, err = MCP("/mcp").MCPTool("t", "").MCPInputSchema(map[string]any{"description": "caf\xe9"}).
		Reply(MCPText("x")).Build(m)
	require.ErrorContains(t, err, "mock: mcp: input schema: ")

	_, ok = errors.AsType[*jsontext.SyntacticError](err)
	require.True(t, ok, "%v is no invalid UTF-8 error", err)
}

func TestMCP_OneBuilderBuildsTheSameMockTwice(t *testing.T) {
	// A builder gives each server a mock of its own, and stays as it was: the matchers aren't added twice.
	tool := MCP("/mcp").ID("t").MCPTool("t", "").MCPArgument("a", match.Equal(1)).Reply(MCPText("ok"))

	descriptions := make([]string, 0, 2)

	for range 2 {
		m := startMCP(t, nil, MCP("/mcp").MCPServerInfo("s", "1"))
		scope := m.MustMock(tool)

		desc, err := scope.Mock("t").MarshalJSON()
		require.NoError(t, err)

		descriptions = append(descriptions, string(desc))

		res, body := mcpPost(t, m.URL("/mcp"), toolCall("t", `{"a":1}`))
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`, body)
	}

	require.JSONEq(t, descriptions[0], descriptions[1])
	require.JSONEq(t, `{"id":"t","mcp":{"endpoint":"/mcp","tool":{"name":"t","arguments":{"a":["eq",1]}}},`+
		`"mcp_result":{"text":"ok"}}`, descriptions[0])
}

func TestMCP_ToolMockMatchesItsEndpointToolAndArguments(t *testing.T) {
	m := startMCP(t, nil,
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").MCPTool("get_user", "").MCPArgument("id", match.Equal(1)).Reply(MCPText("ada")),
		MCP("/mcp/x").MCPTool("get_user", "").Reply(MCPText("elsewhere")),
	)

	res, body := mcpPost(t, m.URL("/mcp"), toolCall("get_user", `{"id":1}`))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ada"}]}}`, body)

	// Another argument value, or another tool, matches no tool mock: the endpoint's path answers a JSON-RPC error.
	for _, call := range []string{toolCall("get_user", `{"id":2}`), toolCall("other", `{}`)} {
		res, body = mcpPost(t, m.URL("/mcp"), call)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Contains(t, body, `"code":-32602`)
	}

	// Each tool mock answers its own path only.
	res, body = mcpPost(t, m.URL("/mcp/x"), toolCall("get_user", `{"id":2}`))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"elsewhere"}]}}`, body)

	// A path without an endpoint mock answers as any request no mock matches.
	res, body = mcpPost(t, m.URL("/mcp/y"), toolCall("get_user", `{"id":1}`))
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.NotContains(t, body, `"jsonrpc"`)
}

func TestMCP_LargeIntegerArgumentsMatchExactly(t *testing.T) {
	// An integer past 2^53 keeps every digit, nested ones too: it matches its own mock, never its neighbor's.
	m := startMCP(t, nil,
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").MCPTool("exact", "").MCPArgument("id", match.Equal(int64(9007199254740993))).Reply(MCPText("exact")),
		MCP("/mcp").MCPTool("rounded", "").MCPArgument("id", match.Equal(int64(9007199254740992))).
			Reply(MCPText("rounded")),
		MCP("/mcp").MCPTool("nested", "").
			MCPArgument("filter", match.Equal(map[string]any{"id": int64(9007199254740993)})).Reply(MCPText("nested")),
	)

	tests := []struct {
		name string
		call string
		want string
	}{
		{
			name: "the same integer",
			call: toolCall("exact", `{"id":9007199254740993}`),
			want: `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"exact"}]}}`,
		},
		{
			name: "the neighbor",
			call: toolCall("rounded", `{"id":9007199254740993}`),
			want: `"message":"mcp: no mock of tool \"rounded\" matched the call"`,
		},
		{
			name: "the same nested integer",
			call: toolCall("nested", `{"filter":{"id":9007199254740993}}`),
			want: `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"nested"}]}}`,
		},
		{
			name: "the nested neighbor",
			call: toolCall("nested", `{"filter":{"id":9007199254740992}}`),
			want: `"message":"mcp: no mock of tool \"nested\" matched the call"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := mcpPost(t, m.URL("/mcp"), tt.call)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Contains(t, body, tt.want)
		})
	}
}

func TestMCP_ArgumentNameWithDotMatches(t *testing.T) {
	m := startMCP(t, nil, MCP("/mcp").MCPTool("t", "").MCPArgument("a.b", match.Equal("x")).Reply(MCPText("ok")))

	res, _ := mcpPost(t, m.URL("/mcp"), toolCall("t", `{"a.b":"x"}`))
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = mcpPost(t, m.URL("/mcp"), toolCall("t", `{"a":{"b":"x"}}`))
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestMCP_RequestPatternVerifiesToolCalls(t *testing.T) {
	// A verify pattern made with MCP matches the calls of its tool, as the mock does.
	m := startMCP(t, []Option{Setup().Journal(t.TempDir())},
		MCP("/mcp").MCPServerInfo("s", "1"),
		MCP("/mcp").MCPTool("get_user", "").Reply(MCPText("ada")),
		MCP("/mcp").MCPTool("list_posts", "").Reply(MCPText("[]")),
	)

	mcpPost(t, m.URL("/mcp"), toolCall("get_user", `{}`))
	mcpPost(t, m.URL("/mcp"), toolCall("list_posts", `{}`))

	n, err := m.VerifyRequests().Matching(MCP("/mcp").MCPTool("get_user", "")).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
