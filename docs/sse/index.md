# SSE (Server-sent Events)

An HTTP mock replies with [server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html): a `text/event-stream` body, sent event by event, each flushed to the client as it goes. Use it to test clients of streamed LLM answers, live feeds, job progress, notifications and MCP servers over Streamable HTTP.

::: code-group

```go [Go]
m.MustMock(mockhttp.Getf("/prices").Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().Name("price").ID("1").DataJSON(map[string]any{"value": 10}),
	mockhttp.NewEvent().Name("price").ID("2").DataJSON(map[string]any{"value": 11}).Delay(time.Second),
))))
```

```yaml [Mock file]
request: {method: GET, path: /prices}
response:
  status: 200
  event_stream:
    events:
      - {event: price, id: "1", data: {value: 10}}
      - {event: price, id: "2", data: {value: 11}, delay: 1s}
```

```sh [CLI]
vlatte --addr=127.0.0.1:8080   # with the mock file under testdata/vlatte/_mocks
curl -N http://127.0.0.1:8080/prices
```

:::

The client gets the first event at once, and the second a second later:

```text
event: price
id: 1
data: {"value":10}

event: price
id: 2
data: {"value":11}

```

## What the reply sends

- **Headers.** `Content-Type: text/event-stream` and `Cache-Control: no-cache`, unless the reply sets them: a `Content-Type` header can add a charset, or name another type to test a client that refuses it. The headers go out before the first event, so a client opens the stream even when nothing is due yet.
- **Flushing.** Each event is flushed as it goes, over HTTP/1.1 and HTTP/2, and gzipped too, with `Gzip()` or `encoding: gzip`.
- **HEAD.** A `HEAD` request gets the headers only.
- **Status.** A stream needs a status with a body: a 1xx, `204` or `304` fails the mock.
- **Ending.** A stream ends after its last event, unless it [keeps open](/sse/timing#keeping-open). The mock counts the request [once the stream starts](/sse/timing#how-a-stream-counts).

A mock with an event stream is a mock as any other: it matches requests, takes part in [scenarios](/scenarios/), runs [callbacks and events](/webhooks/), and can be added over the [admin API](/admin-api/) and [MCP](/mcp/). Only HTTP mocks reply with events: gRPC has [streams](/grpc/streaming) of its own.

::: tip
The admin API's `GET /events` and `GET /logs` are server-sent events too, but of vlatte itself: what the server serves and logs. See [Admin API](/admin-api/#routes).
:::

## Pages

| Page | What it covers |
|---|---|
| [Events](/sse/events) | The fields of an event, text and JSON data, and templated events. |
| [Timing and lifetime](/sse/timing) | Delays, heartbeats, streams that keep open, and how a stream counts. |
| [Reconnecting](/sse/reconnecting) | `retry`, `Last-Event-ID`, resuming, and telling clients to stop. |
| [Testing a client's parser](/sse/raw-events) | Raw events, cut connections, and other faults. |
| [With other features](/sse/other-features) | Proxying, recording, CORS, the admin API and per-user feeds. |

Two guides put the parts together: [Testing an LLM streaming client](/guides/llm-streaming) and [Mocking a remote MCP server](/guides/mcp-server).
