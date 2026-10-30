# Timing and lifetime

A stream sends its events at once, unless they have delays, and ends after the last one, unless it keeps open.

::: code-group

```go [Go]
mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().Name("price").Data("10"),
	mockhttp.NewEvent().Name("price").Data("11").Delay(30*time.Second),
).EventDelay(vlatte.Latency(100*time.Millisecond)).Heartbeat(15*time.Second).KeepOpen())
```

```yaml [Mock file]
response:
  status: 200
  event_stream:
    event_delay: 100ms  # before each event without a delay of its own
    heartbeat: 15s      # a comment each time the stream waits this long
    keep_open: true     # stay open after the last event, until the client leaves
    events:
      - event: price
        data: "10"
      - event: price
        data: "11"
        delay: 30s
```

:::

## Delays

| Go | Mock file | Waits |
|---|---|---|
| `EventDelay(delay)` on the stream | `event_delay` | A duration chosen before each event without a delay of its own. Use any [delay distribution](/more/mock-lifecycle#delays). |
| `Delay(d)` on an event | `delay` | `d` before that event, in place of the stream's. |

Everything before a wait is flushed first, so the client reads the events before it while it waits. Built-in event delays describe themselves, including distributions. A `vlatte.DelayFunc` cannot be serialized, so describing its mock fails explicitly. Evaluation errors end the stream and are reported as reply failures.

These delays are between events. The mock's own [delay](/more/mock-lifecycle#delays) waits before the reply starts, and [pipes](/http/pipes) can slow the bytes of the stream.

## Heartbeats

`Heartbeat(d)`, `heartbeat` in a mock file, sends a comment, `: heartbeat`, each time the stream waits `d` without sending anything: while it waits for a delayed event, and while it keeps open after the last one. Clients skip comments, and proxies and load balancers that drop a silent connection keep it.

## Keeping open

`KeepOpen()`, `keep_open: true`, keeps the stream open after its last event, as a live feed is, until the client leaves or the server closes. A stream without it ends after its last event, and a client such as `EventSource` [reconnects](/sse/reconnecting) after its `retry` time.

A stream that keeps open with no events at all sends its headers at once, so the client opens it, then only its heartbeats.

## How a stream counts

A stream may stay open as long as its client does, so the mock counts the request once the stream starts, not once it ends:

- `Once()` and `Times(n)` serve one client, or `n`, while their streams are still open. The next request goes to another mock.
- A [scenario](/scenarios/) moves on once the stream starts, so the next request matches in the new state while the stream is open.
- `Hits()` and `AssertCalled` count open streams.

The mock runs its [callbacks and `on_complete` events](/webhooks/) once the stream ends, with the body it sent, when that is 1 MiB or less.

```go
feed := m.MustMock(mockhttp.Getf("/feed").Once().
	Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(mockhttp.NewEvent().Data("hi")).KeepOpen())))
m.MustMock(mockhttp.Getf("/feed").Priority(1).Reply(mockhttp.Status(http.StatusConflict)))
// The first client gets the feed, and keeps it open. While it does, feed.Hits() is 1,
// and a second client gets 409.
```

Each request gets a stream of its own, from the first event: a mock serves many clients at once, such as every open tab of a dashboard, each with its own delays and its own end.
