package github_test

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

var _ http.RoundTripper = (*bearerTransport)(nil)

// _endpoint is the path of the MCP server.
const _endpoint = "/mcp/"

var (
	_user = map[string]any{
		"login":    "octocat",
		"id":       1,
		"name":     "The Octocat",
		"html_url": "https://github.com/octocat",
	}
	_issue = map[string]any{
		"number":   42,
		"title":    "Found a bug",
		"state":    "open",
		"user":     map[string]any{"login": "octocat"},
		"html_url": "https://github.com/octocat/hello-world/issues/42",
	}
	// _issueReadSchema is the input of GitHub's issue_read tool, which tools/list shows.
	_issueReadSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"method": map[string]any{
				"type":        "string",
				"enum":        []string{"get", "get_comments", "get_sub_issues", "get_parent", "get_labels"},
				"description": "The read operation to perform on a single issue",
			},
			"owner":        map[string]any{"type": "string", "description": "The owner of the repository"},
			"repo":         map[string]any{"type": "string", "description": "The name of the repository"},
			"issue_number": map[string]any{"type": "number", "description": "The number of the issue"},
			"page":         map[string]any{"type": "number", "description": "Page number for pagination (min 1)"},
			"perPage":      map[string]any{"type": "number", "description": "Results per page for pagination (min 1, max 100)"},
		},
		"required": []string{"method", "owner", "repo", "issue_number"},
	}
)

func TestGoMocksAnswerAnMCPClientAsGitHubDoes(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.MCP(_endpoint).ID("github").MCPServerInfo("github-mcp-server", "mock"),

		mockhttp.MCP(_endpoint).ID("github-get-me").MCPTool("get_me", "Get my user profile").
			Header("Authorization", match.HasPrefix("Bearer ")).
			Reply(mockhttp.MCPJSON(_user)),

		mockhttp.MCP(_endpoint).ID("github-issue-read").MCPTool("issue_read", "Get issue details").
			MCPInputSchema(_issueReadSchema).
			MCPArgument("method", match.Equal("get")).
			MCPArgument("owner", match.Equal("octocat")).
			MCPArgument("repo", match.Equal("hello-world")).
			MCPArgument("issue_number", match.Equal(42)).
			Header("Authorization", match.HasPrefix("Bearer ")).
			Reply(mockhttp.MCPJSON(_issue)),

		mockhttp.Getf("/api/user").ID("github-rest-user").Reply(mockhttp.OK().JSON(_user)),
	)

	assertGitHubAnswers(t, connect(t, m.URL(_endpoint)))
	requireRESTUser(t, m.URL())
}

func TestMockFileAnswersAnMCPClientAsGitHubDoes(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().MockFilePatterns("testdata/github.mock.yaml"))
	m.MustStart()
	t.Cleanup(m.Close)

	assertGitHubAnswers(t, connect(t, m.URL(_endpoint)))
	requireRESTUser(t, m.URL())
}

// requireRESTUser checks that the REST route of the server at base, beside the MCP server, answers the profile.
func requireRESTUser(t *testing.T, base string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/user", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	want, err := json.Marshal(_user)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(body))
}

// connect opens an MCP session on endpoint, as a client of the GitHub MCP server does: with a bearer token.
func connect(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "example",
		Version: "v1",
	}, nil)

	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Transport: &bearerTransport{token: "test-token"},
		},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}

// assertGitHubAnswers checks that session lists the mocked tools and gets their results.
func assertGitHubAnswers(t *testing.T, session *mcp.ClientSession) {
	t.Helper()

	require.Equal(t, "github-mcp-server", session.InitializeResult().ServerInfo.Name)

	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}

	require.Equal(t, []string{"get_me", "issue_read"}, names)

	wantSchema, err := json.Marshal(_issueReadSchema)
	require.NoError(t, err)
	gotSchema, err := json.Marshal(tools.Tools[1].InputSchema)
	require.NoError(t, err)
	require.JSONEq(t, string(wantSchema), string(gotSchema))

	requireToolText(t, session, &mcp.CallToolParams{Name: "get_me"}, _user)
	requireToolText(t, session, &mcp.CallToolParams{
		Name: "issue_read",
		Arguments: map[string]any{
			"method":       "get",
			"owner":        "octocat",
			"repo":         "hello-world",
			"issue_number": 42,
		},
	}, _issue)
}

// requireToolText calls a tool and checks that its result is one text content, the JSON of want.
func requireToolText(t *testing.T, session *mcp.ClientSession, params *mcp.CallToolParams, want any) {
	t.Helper()

	res, err := session.CallTool(t.Context(), params)
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)

	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), text.Text)
}

// bearerTransport adds a bearer token to every request, as GitHub's MCP server expects.
type bearerTransport struct {
	token string
}

func (b *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(r)
}
