package e2e

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

type mcpMocks = []vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]

// _mcpUser is the user of the blog, as its REST route and its get_user tool answer it.
var _mcpUser = map[string]any{"id": 1, "name": "Ada"}

// restAndMCPMocks are the mocks of testdata/mcp_rest: the blog's MCP server at /mcp beside its REST routes.
func restAndMCPMocks() mcpMocks {
	return mcpMocks{
		mockhttp.MCP("/mcp").ID("blog").MCPServerInfo("blog", "1.0.0"),
		mockhttp.MCP("/mcp").ID("get-user").MCPTool("get_user", "Get a user").
			MCPInputSchema(map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "number"}},
				"required":   []any{"id"},
			}).
			MCPArgument("id", match.Equal(1)).
			Reply(mockhttp.MCPJSON(_mcpUser)),
		mockhttp.MCP("/mcp").ID("list-posts").MCPTool("list_posts", "").Reply(mockhttp.MCPJSON([]any{})),
		mockhttp.Getf("/users/1").ID("users").Reply(mockhttp.OK().JSON(_mcpUser)),
		mockhttp.Postf("/posts").ID("posts").Reply(mockhttp.Created().JSON(map[string]any{"id": 7})),
	}
}

// severalMocks are the mocks of testdata/mcp_several: two MCP servers, github and jira, and a REST route.
func severalMocks() mcpMocks {
	return slices.Concat(githubMCPMocks(), mcpMocks{
		mockhttp.MCP("/jira/mcp").ID("jira").MCPServerInfo("jira", "1.0.0"),
		mockhttp.MCP("/jira/mcp").ID("jira-search").MCPTool("search", "").Reply(mockhttp.MCPText("jira")),
		mockhttp.MCP("/jira/mcp").ID("jira-get-issue").MCPTool("get_issue", "").Reply(mockhttp.MCPText("issue")),
		mockhttp.Getf("/users/1").ID("users").Reply(mockhttp.OK().JSON(_mcpUser)),
	})
}

// githubMCPMocks are the github MCP server of severalMocks.
func githubMCPMocks() mcpMocks {
	return mcpMocks{
		mockhttp.MCP("/github/mcp").ID("github").MCPServerInfo("github", "1.0.0"),
		mockhttp.MCP("/github/mcp").ID("github-search").MCPTool("search", "").Reply(mockhttp.MCPText("github")),
		mockhttp.MCP("/github/mcp").ID("github-get-me").MCPTool("get_me", "").Reply(mockhttp.MCPText("me")),
	}
}

// eachSource runs test twice: with mocks built in Go, and with the mock files of testdata/<dir>, which hold the same.
func eachSource(t *testing.T, dir string, mocks mcpMocks, test func(t *testing.T, m *mockhttp.Server)) {
	t.Run("go", func(t *testing.T) { test(t, startMCPMocks(t, mockhttp.Setup(), mocks...)) })
	t.Run("file", func(t *testing.T) {
		test(t, startMCPMocks(t, mockhttp.Setup().MockFilePatterns(filepath.Join("testdata", dir, "*.yaml"))))
	})
}

// startMCPMocks starts a server with setup and mocks, and closes it when the test ends.
func startMCPMocks(t *testing.T, setup *mockhttp.ConfigBuilder, mocks ...vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(setup.Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	if len(mocks) > 0 {
		m.MustMock(mocks...)
	}

	return m
}

// mcpSession connects an MCP client to endpoint, which sends header with every request.
func mcpSession(t *testing.T, endpoint string, header http.Header) *mcp.ClientSession {
	t.Helper()

	return connectMCP(t, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: &headerTransport{header: header}},
	})
}

// headerTransport sends header with every request.
type headerTransport struct {
	header http.Header
}

func (h *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	maps.Copy(r.Header, h.header)

	return http.DefaultTransport.RoundTrip(r)
}

// toolText calls the tool name with args, and returns the text of its one content and whether it is the tool's error.
// It asserts nothing, so goroutines can call it.
func toolText(ctx context.Context, cs *mcp.ClientSession, name string, args any) (string, bool, error) {
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", false, err
	}

	if len(res.Content) != 1 {
		return "", res.IsError, errors.New("expected one content")
	}

	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return "", res.IsError, errors.New("expected a text content")
	}

	return text.Text, res.IsError, nil
}

// requireToolText calls the tool name with args, and requires its text to be want.
func requireToolText(t *testing.T, cs *mcp.ClientSession, name string, args any, want string) {
	t.Helper()

	text, isError, err := toolText(t.Context(), cs, name, args)
	require.NoError(t, err)
	require.False(t, isError)
	require.Equal(t, want, text)
}

// toolError calls the tool name with args, which must fail with a JSON-RPC error, and returns it.
func toolError(t *testing.T, cs *mcp.ClientSession, name string, args any) *jsonrpc.Error {
	t.Helper()

	_, _, err := toolText(t.Context(), cs, name, args)
	require.Error(t, err)

	e, ok := errors.AsType[*jsonrpc.Error](err)
	require.True(t, ok, "%v is no JSON-RPC error", err)

	return e
}

// toolNames lists the names of the tools of cs.
func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}

	return names
}

// postJSONRPC sends msg to path of m as an MCP client does, and returns the answer and its body.
func postJSONRPC(t *testing.T, m *mockhttp.Server, path, msg string) (*http.Response, string) {
	t.Helper()

	return sendBody(t, m, http.MethodPost, path, mockhttp.MIMEApplicationJSON, msg)
}

func TestMCPMock_RESTAndMCPOnOneServer(t *testing.T) {
	// An MCP server is often one route of a larger API: its mocks and the API's share a server.
	eachSource(t, "mcp_rest", restAndMCPMocks(), func(t *testing.T, m *mockhttp.Server) {
		res, body := respond(t, m, http.MethodGet, "/users/1", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.JSONEq(t, `{"id":1,"name":"Ada"}`, body)

		res, body = sendBody(t, m, http.MethodPost, "/posts", mockhttp.MIMEApplicationJSON, `{"title":"x"}`)
		require.Equal(t, http.StatusCreated, res.StatusCode)
		require.JSONEq(t, `{"id":7}`, body)

		cs := mcpSession(t, m.URL("/mcp"), nil)
		require.Equal(t, "blog", cs.InitializeResult().ServerInfo.Name)
		require.Equal(t, []string{"get_user", "list_posts"}, toolNames(t, cs))
		requireToolText(t, cs, "get_user", map[string]any{"id": 1}, `{"id":1,"name":"Ada"}`)
	})
}

func TestMCPMock_MCPDoesNotCatchREST(t *testing.T) {
	// The MCP mocks answer their path only: a request elsewhere, even a JSON-RPC one, is the server's no-match.
	eachSource(t, "mcp_rest", restAndMCPMocks(), func(t *testing.T, m *mockhttp.Server) {
		res, _ := respond(t, m, http.MethodGet, "/comments", "")
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

		res, body := postJSONRPC(t, m, "/users",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_user"}}`)
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
		require.NotContains(t, body, `"jsonrpc"`)
	})
}

func TestMCPMock_NestedUnderRESTPrefix(t *testing.T) {
	mocks := mcpMocks{
		mockhttp.MCP("/api/mcp").ID("blog").MCPServerInfo("blog", "1.0.0"),
		mockhttp.MCP("/api/mcp").ID("get-user").MCPTool("get_user", "").Reply(mockhttp.MCPJSON(_mcpUser)),
		mockhttp.Getf("/api/users").ID("users").Reply(mockhttp.OK().JSON([]any{})),
	}

	eachSource(t, "mcp_nested", mocks, func(t *testing.T, m *mockhttp.Server) {
		res, body := respond(t, m, http.MethodGet, "/api/users", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.JSONEq(t, `[]`, body)

		requireToolText(t, mcpSession(t, m.URL("/api/mcp"), nil), "get_user", nil, `{"id":1,"name":"Ada"}`)
	})
}

func TestMCPMock_ExactEndpointPath(t *testing.T) {
	// /mcp is an MCP server's path, and only that path: a route under it, and a request to /mcp/v2, aren't its.
	mocks := mcpMocks{
		mockhttp.MCP("/mcp").ID("blog").MCPServerInfo("blog", "1.0.0"),
		mockhttp.Getf("/mcp/v2/status").ID("status").Reply(mockhttp.OK().BodyText("ok")),
	}

	eachSource(t, "mcp_exact", mocks, func(t *testing.T, m *mockhttp.Server) {
		res, body := respond(t, m, http.MethodGet, "/mcp/v2/status", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "ok", body)

		res, body = postJSONRPC(t, m, "/mcp/v2", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
		require.NotContains(t, body, `"jsonrpc"`)
	})
}

func TestMCPMock_UnknownToolKeepsSessionAndNamesNearMiss(t *testing.T) {
	// A call no tool mock matches is a JSON-RPC error, which the client takes without dropping its session, and which
	// names the mock that came closest.
	eachSource(t, "mcp_rest", restAndMCPMocks(), func(t *testing.T, m *mockhttp.Server) {
		cs := mcpSession(t, m.URL("/mcp"), nil)

		e := toolError(t, cs, "get_user", map[string]any{"id": 2})
		require.Equal(t, int64(-32602), e.Code)
		require.Equal(t, `mcp: no mock of tool "get_user" matched the call`, e.Message)
		require.Contains(t, string(e.Data), `"mock_id":"get-user"`)
		require.Contains(t, string(e.Data), `"mcp arguments.id"`)

		requireToolText(t, cs, "get_user", map[string]any{"id": 1}, `{"id":1,"name":"Ada"}`)
	})
}

func TestMCPMock_ScenarioSharedWithREST(t *testing.T) {
	// A REST request moves a scenario that a tool mock waits on: both kinds of mocks share the server's state.
	mocks := mcpMocks{
		mockhttp.MCP("/mcp").ID("blog").MCPServerInfo("blog", "1.0.0"),
		mockhttp.MCP("/mcp").ID("list-none").MCPTool("list_posts", "").
			ScenarioIs("posts").ScenarioStateIs(matchscenario.StateStarted).
			Reply(mockhttp.MCPJSON([]any{})),
		mockhttp.MCP("/mcp").ID("list-created").MCPTool("list_posts", "").
			ScenarioIs("posts").ScenarioStateIs("created").
			Reply(mockhttp.MCPJSON([]any{map[string]any{"id": 7}})),
		mockhttp.Postf("/posts").ID("posts").
			ScenarioIs("posts").ScenarioStateIs(matchscenario.StateStarted).ScenarioStateWillBe("created").
			Reply(mockhttp.Created().JSON(map[string]any{"id": 7})),
	}

	eachSource(t, "mcp_scenario", mocks, func(t *testing.T, m *mockhttp.Server) {
		cs := mcpSession(t, m.URL("/mcp"), nil)
		requireToolText(t, cs, "list_posts", nil, `[]`)

		res, _ := sendBody(t, m, http.MethodPost, "/posts", mockhttp.MIMEApplicationJSON, `{"title":"x"}`)
		require.Equal(t, http.StatusCreated, res.StatusCode)

		requireToolText(t, cs, "list_posts", nil, `[{"id":7}]`)
	})
}

func TestMCPMock_PerMockVerification(t *testing.T) {
	// Each tool mock counts its own calls, as each REST mock does: the endpoint mock counts the session's lifecycle.
	eachSource(t, "mcp_rest", restAndMCPMocks(), func(t *testing.T, m *mockhttp.Server) {
		requireToolText(t, mcpSession(t, m.URL("/mcp"), nil), "get_user", map[string]any{"id": 1}, `{"id":1,"name":"Ada"}`)

		res, _ := respond(t, m, http.MethodGet, "/users/1", "")
		require.Equal(t, http.StatusOK, res.StatusCode)

		require.ElementsMatch(t, []string{"blog", "get-user", "users"}, mockIDs(m.ListCalled()))
		require.ElementsMatch(t, []string{"list-posts", "posts"}, mockIDs(m.ListPending()))
	})
}

// mockIDs returns the IDs of mocks.
func mockIDs(mocks []*mockhttp.Mock) []string {
	ids := make([]string, 0, len(mocks))
	for _, m := range mocks {
		ids = append(ids, m.ID())
	}

	return ids
}

func TestMCPMock_AdminAddsToolAtRuntime(t *testing.T) {
	// A tool mock the admin API adds while a client works shows in tools/list and answers.
	m := startMCPMocks(t, mockhttp.Setup().Admin(admin.MustNew()), restAndMCPMocks()...)

	status, body := sendAdmin(t, m, http.MethodPost, "/mocks",
		`{"id":"added","mcp":{"endpoint":"/mcp","tool":{"name":"added"}},"mcp_result":{"text":"ok"}}`)
	require.Equal(t, http.StatusCreated, status, body)

	cs := mcpSession(t, m.URL("/mcp"), nil)
	require.Equal(t, []string{"get_user", "list_posts", "added"}, toolNames(t, cs))
	requireToolText(t, cs, "added", nil, "ok")

	res, _ := respond(t, m, http.MethodGet, "/users/1", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestMCPMock_Reload(t *testing.T) {
	// Editing the mock files and reloading changes the tools and the REST routes in one step.
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "mcp_rest"))))
	require.NoError(t, os.Remove(filepath.Join(dir, "40_posts.yaml")))

	m := startMCPMocks(t, mockhttp.Setup().MockFilePatterns(filepath.Join(dir, "*.yaml")))
	require.Equal(t, []string{"get_user", "list_posts"}, toolNames(t, mcpSession(t, m.URL("/mcp"), nil)))

	require.NoError(t, os.Remove(filepath.Join(dir, "20_list_posts.yaml")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "30_users.yaml"), []byte(
		"id: users\nrequest: {method: GET, path: /users/1}\nresponse: {status: 200, body: {id: 1, name: Bob}}\n"), 0o600))
	require.NoError(t, m.Reload())

	require.Equal(t, []string{"get_user"}, toolNames(t, mcpSession(t, m.URL("/mcp"), nil)))

	_, body := respond(t, m, http.MethodGet, "/users/1", "")
	require.JSONEq(t, `{"id":1,"name":"Bob"}`, body)
}

func TestMCPMock_SeveralServers(t *testing.T) {
	// One server plays several MCP servers, each at its path, with tools of the same name.
	eachSource(t, "mcp_several", severalMocks(), func(t *testing.T, m *mockhttp.Server) {
		github := mcpSession(t, m.URL("/github/mcp"), nil)
		jira := mcpSession(t, m.URL("/jira/mcp"), nil)

		require.Equal(t, "github", github.InitializeResult().ServerInfo.Name)
		require.Equal(t, "jira", jira.InitializeResult().ServerInfo.Name)
		require.Equal(t, []string{"search", "get_me"}, toolNames(t, github))
		require.Equal(t, []string{"search", "get_issue"}, toolNames(t, jira))

		requireToolText(t, github, "search", nil, "github")
		requireToolText(t, jira, "search", nil, "jira")

		res, _ := respond(t, m, http.MethodGet, "/users/1", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestMCPMock_SeveralServersConcurrently(t *testing.T) {
	// Clients of two MCP servers call at once: each call gets its own server's answer.
	eachSource(t, "mcp_several", severalMocks(), func(t *testing.T, m *mockhttp.Server) {
		sessions := map[string]*mcp.ClientSession{
			"github": mcpSession(t, m.URL("/github/mcp"), nil),
			"jira":   mcpSession(t, m.URL("/jira/mcp"), nil),
		}

		type result struct {
			want string
			text string
			err  error
		}

		const calls = 20

		results := make(chan result, calls*len(sessions))

		var wg sync.WaitGroup
		for name, cs := range sessions {
			for range calls {
				wg.Go(func() {
					text, _, err := toolText(t.Context(), cs, "search", nil)
					results <- result{want: name, text: text, err: err}
				})
			}
		}

		wg.Wait()
		close(results)

		n := 0
		for r := range results {
			require.NoError(t, r.err)
			require.Equal(t, r.want, r.text)
			n++
		}

		require.Equal(t, calls*len(sessions), n)
	})
}

func TestMCPMock_SeveralServersFallbacksStayOwn(t *testing.T) {
	eachSource(t, "mcp_several", severalMocks(), func(t *testing.T, m *mockhttp.Server) {
		// get_issue is jira's: github has no mock of it.
		e := toolError(t, mcpSession(t, m.URL("/github/mcp"), nil), "get_issue", nil)
		require.Equal(t, int64(-32602), e.Code)

		res, body := postJSONRPC(t, m, "/jira/mcp", `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`, body)
	})
}

func TestMCPMock_SeveralServersOnceIsPerServer(t *testing.T) {
	mocks := slices.Clone(severalMocks())
	mocks[1] = mockhttp.MCP("/github/mcp").ID("github-search").MCPTool("search", "").Once().
		Reply(mockhttp.MCPText("github"))

	m := startMCPMocks(t, mockhttp.Setup(), mocks...)
	github := mcpSession(t, m.URL("/github/mcp"), nil)
	jira := mcpSession(t, m.URL("/jira/mcp"), nil)

	requireToolText(t, github, "search", nil, "github")
	require.Equal(t, int64(-32602), toolError(t, github, "search", nil).Code)
	requireToolText(t, jira, "search", nil, "jira")
}

func TestMCPMock_RemoveOneServer(t *testing.T) {
	// Unmocking one MCP server leaves the other, and the REST routes, as they were.
	m := startMCPMocks(t, mockhttp.Setup(), severalMocks()[3:]...)
	github := m.MustMock(githubMCPMocks()...)

	require.NoError(t, m.Unmock(t.Context(), github))

	requireToolText(t, mcpSession(t, m.URL("/jira/mcp"), nil), "search", nil, "jira")

	res, _ := respond(t, m, http.MethodGet, "/users/1", "")
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, body := postJSONRPC(t, m, "/github/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.NotContains(t, body, `"jsonrpc"`)
}

// echoServer starts an MCP server with a tool, echo, that answers its msg argument and counts its calls in calls. It
// needs no session, so a tool mock can forward one call to it.
func echoServer(t *testing.T, calls *atomic.Int64) *httptest.Server {
	t.Helper()

	up := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "1"}, nil)
	mcp.AddTool(up, &mcp.Tool{Name: "echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, in struct {
			Msg string `json:"msg"`
		},
		) (*mcp.CallToolResult, any, error) {
			calls.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Msg}}}, nil, nil
		})

	s := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return up },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	t.Cleanup(s.Close)

	return s
}

func TestMCPMock_ProxiedTool(t *testing.T) {
	// A tool mock can forward its calls to a real MCP server, as any mock can proxy its requests.
	var calls atomic.Int64

	upstream := echoServer(t, &calls)

	t.Run("go", func(t *testing.T) {
		calls.Store(0)

		m := startMCPMocks(t, mockhttp.Setup(),
			mockhttp.MCP("/mcp").MCPServerInfo("blog", "1.0.0"),
			mockhttp.MCP("/mcp").MCPTool("echo", "").Reply(mockhttp.From(upstream.URL)),
		)

		requireToolText(t, mcpSession(t, m.URL("/mcp"), nil), "echo", map[string]any{"msg": "hi"}, "hi")
		require.Equal(t, int64(1), calls.Load())
	})

	t.Run("file", func(t *testing.T) {
		calls.Store(0)

		dir := t.TempDir()
		data, err := os.ReadFile(filepath.Join("testdata", "mcp_rest", "00_endpoint.yaml"))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "00_endpoint.yaml"), data, 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "10_echo.yaml"), []byte(
			"mcp: {endpoint: /mcp, tool: {name: echo}}\nproxy: {target: '"+upstream.URL+"'}\n"), 0o600))

		m := startMCPMocks(t, mockhttp.Setup().MockFilePatterns(filepath.Join(dir, "*.yaml")))

		requireToolText(t, mcpSession(t, m.URL("/mcp"), nil), "echo", map[string]any{"msg": "hi"}, "hi")
		require.Equal(t, int64(1), calls.Load())
	})
}

func TestMCPMock_MockedAndProxiedToolsSideBySide(t *testing.T) {
	var calls atomic.Int64

	upstream := echoServer(t, &calls)

	m := startMCPMocks(t, mockhttp.Setup(),
		mockhttp.MCP("/mcp").MCPServerInfo("blog", "1.0.0"),
		mockhttp.MCP("/mcp").MCPTool("echo", "").Reply(mockhttp.From(upstream.URL)),
		mockhttp.MCP("/mcp").MCPTool("get_me", "").Reply(mockhttp.MCPText("me")),
	)

	cs := mcpSession(t, m.URL("/mcp"), nil)
	require.Equal(t, []string{"echo", "get_me"}, toolNames(t, cs))

	requireToolText(t, cs, "get_me", nil, "me")
	require.Equal(t, int64(0), calls.Load())

	requireToolText(t, cs, "echo", map[string]any{"msg": "hi"}, "hi")
	require.Equal(t, int64(1), calls.Load())
}

func TestMCPMock_HTTPFeaturesOnToolMocks(t *testing.T) {
	// A tool mock is a mock: a header matcher, Times and a sequence of replies work on it as on a REST mock.
	m := startMCPMocks(t, mockhttp.Setup(),
		mockhttp.MCP("/mcp").MCPServerInfo("blog", "1.0.0"),
		mockhttp.MCP("/mcp").MCPTool("count", "").Header("X-Team", match.Equal("a")).Times(2).
			Reply(mockhttp.Seq(mockhttp.MCPText("1"), mockhttp.MCPText("2"))),
	)

	// First, without the header, so the miss is the header's, not Times'.
	require.Equal(t, int64(-32602), toolError(t, mcpSession(t, m.URL("/mcp"), nil), "count", nil).Code)

	team := mcpSession(t, m.URL("/mcp"), http.Header{"X-Team": {"a"}})
	requireToolText(t, team, "count", nil, "1")
	requireToolText(t, team, "count", nil, "2")
	require.Equal(t, int64(-32602), toolError(t, team, "count", nil).Code)
}
