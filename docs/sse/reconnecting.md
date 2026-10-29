# Reconnecting

A client such as `EventSource` reconnects when a stream ends or its connection drops: it waits the `retry` time the stream set, and sends the id of the last event it got as the `Last-Event-ID` header. A mock serves that reconnection like any request: resume the stream where it was, answer with another mock, or tell the client to stop.

::: code-group

```go [Go]
mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().ID("1").Data("a"),
	mockhttp.NewEvent().ID("2").Data("b"),
	mockhttp.NewEvent().ID("3").Data("c"),
).Retry(3 * time.Second).ResumeFromLastEventID())
```

```yaml [Mock file]
response:
  status: 200
  event_stream:
    retry: 3s     # sent first: how long clients wait before they reconnect
    resume: true  # a Last-Event-ID gets only the events after that id
    events:
      - {id: "1", data: a}
      - {id: "2", data: b}
      - {id: "3", data: c}
```

:::

## Retry

`EventStream.Retry(d)`, `retry` beside `events`, sends a `retry` field before the first event: how long the client waits before it reconnects. An event's own `retry` changes it from that event on. Clients read it in whole milliseconds, so `1.5s` goes as `retry: 1500`.

## Resuming

With `ResumeFromLastEventID()`, `resume: true`, a request with a `Last-Event-ID` header gets only the events after the one with that id: above, `Last-Event-ID: 1` gets `b` and `c`. An id no event has gets them all, and so does a request without the header. The stream's `retry` is sent either way.

Only ids set with `ID`, or `id` in a mock file, count: an `id` line inside a [raw event](/sse/raw-events) is sent, but resuming doesn't know it.

## Answering a reconnection with another mock

Match the `Last-Event-ID` header to give a reconnection another answer, such as the rest of a stream, or a different one:

::: code-group

```go [Go]
m.MustMock(mockhttp.Getf("/jobs/7/progress").
	Header(mockhttp.HeaderLastEventID, match.Equal("3")).
	Reply(mockhttp.NoContent()))
```

```yaml [Mock file]
request:
  method: GET
  path: /jobs/7/progress
  header: {last-event-id: "3"}
response:
  status: 204
```

:::

## Telling clients to stop

A stream that ends makes `EventSource` reconnect, even when the server is done. Answer that reconnection with `204 No Content`, a reply without `event_stream`, as above, and the client stops.

An answer that isn't a stream also stops it: a status other than `200` after redirects, or a `Content-Type` other than `text/event-stream`. A mock that answers `401` to a request without credentials, before any stream starts, makes `EventSource` give up rather than retry.

## Testing a reconnection

Cut the first stream with [`fault.LimitData`](/http/faults), so the connection drops after the events a client should keep, and resume the stream on the reconnection:

::: code-group

```go [Go]
stream := mockhttp.NewEventStream(
	mockhttp.NewEvent().ID("1").Data("a"),
	mockhttp.NewEvent().ID("2").Data("b"),
	mockhttp.NewEvent().ID("3").Data("c"),
).Retry(1500 * time.Millisecond).ResumeFromLastEventID()

// The first connection drops after the retry field and event 1.
m.MustMock(mockhttp.Getf("/events").Once().
	Pipe(fault.LimitData(len("retry: 1500\n\nid: 1\ndata: a\n\n"))).
	Reply(mockhttp.OK().EventStream(stream)))
// The reconnection gets events 2 and 3: nothing is lost, nothing sent twice.
m.MustMock(mockhttp.Getf("/events").
	Header(mockhttp.HeaderLastEventID, match.Equal("1")).
	Reply(mockhttp.OK().EventStream(stream)))
```

```yaml [drop.yaml]
# The first connection drops after the retry field and event 1.
request: {method: GET, path: /events}
repeat: 1
pipes: [{type: limit_data, bytes: 28}]
response:
  status: 200
  event_stream:
    retry: 1500ms
    resume: true
    events:
      - {id: "1", data: a}
      - {id: "2", data: b}
      - {id: "3", data: c}
```

```yaml [resume.yaml]
# The reconnection gets events 2 and 3.
request:
  method: GET
  path: /events
  header: {last-event-id: "1"}
response:
  status: 200
  event_stream:
    retry: 1500ms
    resume: true
    events:
      - {id: "1", data: a}
      - {id: "2", data: b}
      - {id: "3", data: c}
```

:::

The client must reconnect after the drop with `Last-Event-ID: 1`, and end with `a`, `b` and `c`, once each.
