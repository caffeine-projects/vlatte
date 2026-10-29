# Mocking a remote MCP server

This guide mocks a remote [MCP](https://modelcontextprotocol.io) server, a weather server over Streamable HTTP, with mock files, and tests an MCP client against it: the client connects, calls a tool, gets its progress and its result. It uses [event streams](/sse/), [templates](/templating/) and [JSON matching](/http/matching#json-and-xml-bodies).

::: tip
This is vlatte playing an MCP server that your code calls. For vlatte serving MCP to your AI agent, so the agent can drive the mock server, see [MCP](/mcp/).
:::

## How Streamable HTTP talks

An MCP client sends each JSON-RPC message as a `POST` to one endpoint, `/mcp` here. The server answers a request with JSON, or with an event stream that sends messages, such as progress, before the answer. It starts a session in its answer to `initialize`, with an `Mcp-Session-Id` header, which the client sends with every later request. The client also listens on a `GET` of the endpoint for what the server sends on its own, and ends the session with a `DELETE`.

Each message is a mock that matches the method of the JSON-RPC body with `jsonpath`, so every mock gets its own requests at the one endpoint.

## The mock files

`initialize` answers with an event stream. Its one event is the result, a template that takes the request's `id` and agrees to the protocol version the client asked for:

```yaml
# testdata/mcp/initialize.yaml
id: mcp-initialize
request:
  method: POST
  path: /mcp
  header:
    accept: [contains, text/event-stream]
  body: [jsonpath, $.method, [equal, initialize]]
response:
  status: 200
  header:
    mcp-session-id: weather-1
  event_stream:
    events:
      - event: message
        data: >-
          {{ $r := fromJSON .Request.Body.String }}{"jsonrpc": "2.0", "id": {{ $r.id | toJSON }}, "result": {
          "protocolVersion": {{ $r.params.protocolVersion | toJSON }},
          "capabilities": {"tools": {"listChanged": true}},
          "serverInfo": {"name": "weather", "version": "1.0.0"}}}
  template: {enabled: true}
```

A notification has no answer but `202 Accepted`:

```yaml
# testdata/mcp/initialized.yaml
id: mcp-initialized
request:
  method: POST
  path: /mcp
  header:
    mcp-session-id: weather-1
  body: [jsonpath, $.method, [equal, notifications/initialized]]
response:
  status: 202
```

The Go SDK's client tries `server/discover` before `initialize`. A server built before stateless MCP doesn't know it, and answers with an error, which makes the client fall back to `initialize`:

```yaml
# testdata/mcp/discover.yaml
id: mcp-discover
request:
  method: POST
  path: /mcp
  body: [jsonpath, $.method, [equal, server/discover]]
response:
  status: 200
  header:
    content-type: application/json
  body: '{"jsonrpc": "2.0", "id": {{ (fromJSON .Request.Body.String).id | toJSON }}, "error": {"code": -32601, "message": "Method not found"}}'
  template: {enabled: true}
```

`tools/list` lists one tool, and `tools/call` streams its progress, then its result, on the answer to the call. Both match the session, and the call matches the tool's name too:

```yaml
# testdata/mcp/tools_list.yaml
id: mcp-tools-list
request:
  method: POST
  path: /mcp
  header:
    mcp-session-id: weather-1
  body: [jsonpath, $.method, [equal, tools/list]]
response:
  status: 200
  event_stream:
    events:
      - event: message
        data: >-
          {"jsonrpc": "2.0", "id": {{ (fromJSON .Request.Body.String).id | toJSON }}, "result": {"tools": [{
          "name": "get_weather", "description": "Current weather of a city",
          "inputSchema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}]}}
  template: {enabled: true}
```

```yaml
# testdata/mcp/tools_call.yaml
id: mcp-tools-call
request:
  method: POST
  path: /mcp
  header:
    mcp-session-id: weather-1
  body: [all, [[jsonpath, $.method, [equal, tools/call]], [jsonpath, $.params.name, [equal, get_weather]]]]
response:
  status: 200
  event_stream:
    events:
      - event: message
        data: >-
          {{ $r := fromJSON .Request.Body.String }}{"jsonrpc": "2.0", "method": "notifications/progress", "params": {
          "progressToken": {{ $r.params._meta.progressToken | toJSON }}, "progress": 1, "total": 2,
          "message": {{ printf "looking up %s" $r.params.arguments.city | toJSON }}}}
      - event: message
        data: >-
          {{ $r := fromJSON .Request.Body.String }}{"jsonrpc": "2.0", "id": {{ $r.id | toJSON }}, "result": {
          "content": [{"type": "text", "text": {{ printf "Sunny, 24°C in %s" $r.params.arguments.city | toJSON }}}]}}
  template: {enabled: true}
```

The `data` that holds a template is text, folded with `>-`: data written as a YAML object is sent as JSON, never as a template. See [Templated events](/sse/events#templated-events).

The `GET` the client listens on [keeps open](/sse/timing#keeping-open) for the session, with a heartbeat, and tells the client the tools changed. The `DELETE` ends the session:

```yaml
# testdata/mcp/listen.yaml
id: mcp-listen
request:
  method: GET
  path: /mcp
  header:
    mcp-session-id: weather-1
    last-event-id: [not, present]
response:
  status: 200
  event_stream:
    keep_open: true
    heartbeat: 15s
    events:
      - event: message
        data: {jsonrpc: "2.0", method: notifications/tools/list_changed}
```

```yaml
# testdata/mcp/delete.yaml
id: mcp-delete
request:
  method: DELETE
  path: /mcp
  header:
    mcp-session-id: weather-1
response:
  status: 204
```

## The test

The test loads the mock files, and connects the [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)'s client to the server, as an agent would. It calls the tool with a progress token, so the server's progress reaches the client's handler:

```go
func TestCallsTheWeatherTool(t *testing.T) {
	files, err := filepath.Glob("testdata/mcp/*.yaml")
	if err != nil {
		t.Fatal(err)
	}

	builders := make([]vlatte.Builder[*mockhttp.Mock, *mockhttp.Server], len(files))
	for i, f := range files {
		builders[i] = mockhttp.FromFile(f)
	}

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(builders...)

	progress := make(chan string, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progress <- req.Params.Message
		},
	})

	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: m.URL("/mcp")}, nil)
	if err != nil {
		t.Fatal(err)
	}

	params := &mcp.CallToolParams{Name: "get_weather", Arguments: map[string]any{"city": "Lisbon"}}
	params.SetProgressToken("call-1")

	res, err := session.CallTool(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; text != "Sunny, 24°C in Lisbon" {
		t.Fatalf("text = %q", text)
	}
	if msg := <-progress; msg != "looking up Lisbon" {
		t.Fatalf("progress = %q", msg)
	}

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	m.Close() // waits for the requests it serves, so every call is counted

	if hits := scoped.Mock("mcp-tools-call").Hits(); hits != 1 {
		t.Fatalf("tools/call hits = %d", hits)
	}
}
```

A stream counts once it starts, but a mock that doesn't stream, such as `mcp-delete`, counts its request once it answered, which can be after the client read the answer: closing the server first waits for the requests it serves, so every count is final. The same files, served by the [CLI](/run/cli), mock the server for an agent or a client in any language.

## A call the server closes

A server may close the stream of a long call once it sent an event with an id, to free the connection. The client then reconnects with a `GET` that carries that id as its `Last-Event-ID`, and gets the answer there. Two more mocks play it, with the call's JSON-RPC id as the event id, so the mock that resumes knows what it answers:

```go
m.MustMock(
	mockhttp.Postf("/mcp").ID("forecast-call").
		Header("Mcp-Session-Id", match.Equal("weather-1")).
		Body(match.JSONPath("$.method", match.Equal("tools/call")), match.JSONPath("$.params.name", match.Equal("get_forecast"))).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			mockhttp.NewEvent().ID(`{{ (fromJSON .Request.Body.String).id }}`).Data(""),
		).Retry(10*time.Millisecond).Template())),
	mockhttp.Getf("/mcp").ID("forecast-resume").
		Header("Mcp-Session-Id", match.Equal("weather-1")).
		Header(mockhttp.HeaderLastEventID, match.Present()).
		Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
			mockhttp.NewEvent().Name("message").Data(`{"jsonrpc": "2.0", "id": {{ .Request.Header.Get "Last-Event-ID" }}, `+
				`"result": {"content": [{"type": "text", "text": "Rain tomorrow"}]}}`),
		).Template())),
)
```

The first stream ends after its event, and its `retry` tells the client to reconnect 10 ms later. `listen.yaml` doesn't match the reconnection, as it requires no `Last-Event-ID`. See [Reconnecting](/sse/reconnecting).
