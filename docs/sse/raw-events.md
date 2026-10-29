# Testing a client's parser

Servers in the wild send streams the fields of an event can't write: CRLF or CR line endings, a byte order mark, fields without a space after the colon. A raw event sends its text as it is, to test that a client reads them as the [standard](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation) says.

::: code-group

```go [Go]
mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().Raw("\uFEFFdata: bom\r\n\r\n"),
	mockhttp.NewEvent().Raw("event: cr\rdata:no space\r\r"),
	mockhttp.NewEvent().Raw(": comment\nunknown: x\nretry: soon\ndata: kept\n\n"),
	mockhttp.NewEvent().Raw("id: 7\n\n"),
))
```

```yaml [Mock file]
response:
  status: 200
  event_stream:
    events:
      - {raw: "\uFEFFdata: bom\r\n\r\n"}                     # a byte order mark, CRLF
      - {raw: "event: cr\rdata:no space\r\r"}                # CR, no space after the colon
      - {raw: ": comment\nunknown: x\nretry: soon\ndata: kept\n\n"}  # skipped lines
      - {raw: "id: 7\n\n"}                                   # an id without data
```

:::

A client that follows the standard dispatches three events, `bom`, `no space` of type `cr`, and `kept`, ignores the unknown field and the `retry` that isn't a number, and keeps `7` as its last event ID without dispatching anything.

## Raw events

`Raw(text)`, `raw` in a mock file, sends its text in place of the event's fields. It takes a `delay`, and no other field: a raw event with one fails the mock. Its text needs the blank line that ends an event, unless the test is of an event that never ends.

- **Templates.** A raw event is never a template, in a templated stream too.
- **Ids.** An `id` line in a raw event is sent, and clients read it, but [resuming](/sse/reconnecting#resuming) doesn't know it: it reads only the ids set with `ID` or `id`.
- **Byte order mark.** Clients drop one only as the first bytes of the stream: not after the stream's `retry`, which is sent first, or a heartbeat.

## Cut connections and other faults

[Steps](/http/steps) and [faults](/http/faults) apply to a stream as to any body, after the stream renders it:

| Fault | Tests a client against |
|---|---|
| `fault.LimitData(n)`, `{type: limit_data, bytes: n}` | A connection that drops after `n` bytes, as a flaky network does, such as in the middle of an event: see [Testing a reconnection](/sse/reconnecting#testing-a-reconnection). |
| `fault.Latency(latency, jitter)` | Events that arrive late, at the pace they were sent. |
| `fault.Bandwidth(bytesPerSecond)` | A slow link, which sends a large event in pieces. |
| `fault.Slicer(averageSize, sizeVariation, delay)` | Events split across reads at random. |
| `fault.ConnectionResetByPeerAfter(d)` | A reset `d` after the stream began. |

A fault that holds the whole body, such as `fault.ChunkedDribbleDelay`, waits for a stream that keeps open forever: use it only on a stream that ends.

## Response mappers

A [response mapper](/http/mocks), `Map` in Go, reads the whole body, which a stream may never end: a mock with both fails to build. A stream inside a [sequence or random reply](/http/replies#sequences) fails at the request it answers instead.
