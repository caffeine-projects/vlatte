package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// _mcpSessionID is the header that carries the session of Streamable HTTP.
const _mcpSessionID = "Mcp-Session-Id"

// mockMCPServer starts a server with the mock files of testdata/sse/mcp, a weather MCP server over Streamable HTTP
// at /mcp, and more, and returns the scope of its mocks.
func mockMCPServer(t *testing.T, more ...vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]) (*mockhttp.Server, *vlatte.Scope[*mockhttp.Mock]) {
	t.Helper()

	files, err := filepath.Glob("testdata/sse/mcp/*.yaml")
	require.NoError(t, err)

	builders := make([]vlatte.Builder[*mockhttp.Mock, *mockhttp.Server], 0, len(files)+len(more))
	for _, f := range files {
		builders = append(builders, mockhttp.FromFile(f))
	}

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	return m, m.MustMock(append(builders, more...)...)
}

func TestSSE_MCPAClientListsAndCallsAToolOfAMockedServer(t *testing.T) {
	// An MCP server over Streamable HTTP answers each request with an event stream: progress and other messages,
	// then the answer. The mock files play a weather server, and the official Go SDK client uses it as an agent would:
	// it connects, hears that the tools changed on the stream it listens on, lists the tools, and calls one, which
	// reports its progress before it answers. Closing the client ends the session.
	m, scope := mockMCPServer(t)

	changed := make(chan struct{}, 1)
	progress := make(chan string, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { changed <- struct{}{} },
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progress <- req.Params.Message
		},
	})

	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: m.URL("/mcp")}, nil)
	require.NoError(t, err)
	require.Equal(t, "weather", cs.InitializeResult().ServerInfo.Name)
	require.Equal(t, "weather-1", cs.ID(), "the client keeps the session the server started")

	<-changed

	tools, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	require.Equal(t, "get_weather", tools.Tools[0].Name)

	params := &mcp.CallToolParams{Name: "get_weather", Arguments: map[string]any{"city": "Lisbon"}}
	params.SetProgressToken("call-1")

	res, err := cs.CallTool(t.Context(), params)
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Equal(t, []mcp.Content{&mcp.TextContent{Text: "Sunny, 24°C in Lisbon"}}, res.Content)
	require.Equal(t, "looking up Lisbon", <-progress)

	require.NoError(t, cs.Close())

	// A mock counts a request once it answered, which can be after the client read the answer. Closing the server
	// waits for the requests it serves.
	m.Close()

	for _, id := range []string{"mcp-discover", "mcp-initialize", "mcp-initialized", "mcp-tools-list", "mcp-tools-call", "mcp-delete"} {
		require.EqualValues(t, 1, scope.Mock(id).Hits(), id)
	}
}

func TestSSE_MCPAClientResumesAToolCallTheServerClosed(t *testing.T) {
	// A server may close the stream of a long call once it sent an event with an id, to free the connection. The
	// client reconnects with a GET that carries that id as its Last-Event-ID, after the reconnection time the
	// stream set, and gets the answer there. The event id is the call's id, so the mock that resumes knows what it
	// answers.
	m, scope := mockMCPServer(t,
		mockhttp.Postf("/mcp").ID("forecast-call").
			Header(_mcpSessionID, match.Equal("weather-1")).
			Body(match.JSONPath("$.method", match.Equal("tools/call")), match.JSONPath("$.params.name", match.Equal("get_forecast"))).
			Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
				mockhttp.NewEvent().ID(`{{ (fromJSON .Request.Body.String).id }}`).Data(""),
			).Retry(10*time.Millisecond).Template())),
		mockhttp.Getf("/mcp").ID("forecast-resume").
			Header(_mcpSessionID, match.Equal("weather-1")).
			Header(mockhttp.HeaderLastEventID, match.Present()).
			Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
				mockhttp.NewEvent().Name("message").Data(`{"jsonrpc": "2.0", "id": {{ .Request.Header.Get "Last-Event-ID" }}, `+
					`"result": {"content": [{"type": "text", "text": "Rain tomorrow"}]}}`),
			).Template())),
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: m.URL("/mcp")}, nil)
	require.NoError(t, err)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_forecast", Arguments: map[string]any{"city": "Porto"}})
	require.NoError(t, err)
	require.Equal(t, []mcp.Content{&mcp.TextContent{Text: "Rain tomorrow"}}, res.Content)

	require.NoError(t, cs.Close())
	m.Close() // the requests are counted once they end

	require.EqualValues(t, 1, scope.Mock("forecast-call").Hits())
	require.EqualValues(t, 1, scope.Mock("forecast-resume").Hits())
}
