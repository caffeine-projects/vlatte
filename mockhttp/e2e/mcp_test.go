package e2e

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestMCP_AnAgentMocksARequestAndSeesItServed(t *testing.T) {
	// An agent that writes a test adds the mock it needs, runs the request, and checks the mock answered it, all
	// through the tools of the admin API.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Journal(t.TempDir()).Admin(admin.MustNew(admin.MCP())))
	m.MustStart()
	t.Cleanup(m.Close)

	cs := connectMCP(t, &mcp.StreamableClientTransport{Endpoint: m.AdminURL() + "/mcp"})

	added := callTool(t, cs, "add_mock", map[string]any{"mock": map[string]any{
		"id":       "hello",
		"request":  map[string]any{"method": "GET", "path": "/hello"},
		"response": map[string]any{"status": 200, "body": "hi"},
	}})
	require.Equal(t, "hello", added["id"])

	res, err := http.Get(m.URL("/hello"))
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hi", string(body))

	list := callTool(t, cs, "list_mocks", nil)
	require.Equal(t, []any{map[string]any{
		"id": "hello", "priority": float64(0), "enabled": true, "hits": float64(1), "origin": "admin",
	}}, list["mocks"])

	found := callTool(t, cs, "query_requests", map[string]any{"query": map[string]any{"mock_id": "hello"}})
	require.Equal(t, float64(1), found["count"])
}

func TestMCP_OverStdio(t *testing.T) {
	// An agent that starts the mock server as a process talks to it over stdin and stdout.
	api := admin.MustNew(admin.MCP())
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(api))
	m.MustStart()
	t.Cleanup(m.Close)

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()

	served := make(chan error, 1)
	go func() {
		served <- api.ServeMCP(context.Background(), serverIn, serverOut)
		_ = serverOut.Close()
	}()

	cs := connectMCP(t, &mcp.IOTransport{Reader: clientIn, Writer: clientOut})

	health := callTool(t, cs, "health", nil)
	require.Equal(t, "http", health["kind"])
	require.Equal(t, m.URL(), health["url"])

	require.NoError(t, cs.Close())
	require.NoError(t, <-served, "the end of stdin ends the serving")
}

// connectMCP connects an MCP client over transport, which the test closes.
func connectMCP(t *testing.T, transport mcp.Transport) *mcp.ClientSession {
	t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "v1"}, nil)
	cs, err := client.Connect(t.Context(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// callTool calls the tool name with args, which must succeed, and returns its result.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) map[string]any {
	t.Helper()

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)

	text := res.Content[0].(*mcp.TextContent).Text
	require.False(t, res.IsError, text)

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &doc))

	return doc
}
