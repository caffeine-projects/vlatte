# Server-Sent Events

`Reply.EventStream` serves a `text/event-stream` body event by event, each flushed to the client as it goes, over HTTP/1.1 and HTTP/2. It sets `Content-Type: text/event-stream` and `Cache-Control: no-cache`, unless the reply sets them: a `Content-Type` header can add a charset. A HEAD request gets those headers only. A status without a body, 1xx, 204 or 304, fails the mock.

```go
m.MustMock(mockhttp.Getf("/prices").Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().Name("price").ID("1").DataJSON(map[string]any{"value": 10}),
	mockhttp.NewEvent().Name("price").ID("2").DataJSON(map[string]any{"value": 11}).Delay(time.Second),
).Retry(3*time.Second).Heartbeat(15*time.Second).KeepOpen().ResumeFromLastEventID())))
```

```yaml
response:
  status: 200
  event_stream:
    retry: 3s           # sent first: how long clients wait before they reconnect
    event_delay: 100ms  # before each event without a delay of its own
    heartbeat: 15s      # a comment each time the stream waits this long
    keep_open: true     # stay open after the last event, until the client leaves
    resume: true        # a Last-Event-ID gets only the events after that id
    events:
      - {event: price, id: "1", data: {value: 10}}  # text as is, anything else as JSON
      - {event: price, id: "2", data: {value: 11}, delay: 1s}
      - {comment: "keep-alive"}
      - {data: "line 1\nline 2"}                   # a data line per line
      - {data_json: "done"}                        # always JSON, never a template: sends "done", quotes too
      - {raw: "data: x\r\n\r\n"}                   # sent as it is
```

- **Fields.** `event`, `id`, `retry`, `comment` and `data` or `data_json` go on the wire as the [WHATWG event stream format](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation) reads them. Data and comments with line breaks get a line each, so clients read them back whole. An `event` or `id` with a line break, or an `id` with a NUL, fails the mock, or, in a templated stream, the reply that renders it. An empty `id` is sent: it resets the client's last event ID.
- **Raw.** `raw` (`Raw` in Go) sends its text as it is, in place of the fields but `delay`, to test a client's parser: CRLF or CR line endings, a byte order mark, an event without its blank line. An `id` line in `raw` is sent, but `resume` doesn't know it. A byte order mark counts only as the first bytes of the stream, so not after a `retry` or a heartbeat.
- **Delays.** An event's `delay` wins over `event_delay`. Everything before a wait is flushed first. In Go, `EventDelayFunc` takes a `vlatte.Delay`, such as `vlatte.Jitter`; a description leaves it out.
- **Ending.** A stream ends after its last event, and clients reconnect after `retry`. With `keep_open`, it ends when the client leaves or the server closes. The mock counts the request once the stream starts, so `times` and scenarios see an open stream, and runs its callbacks and `on_complete` events once it ends, with the body it sent.
- **Reconnecting.** `resume` sends a request with a `Last-Event-ID` header only the events after the one with that id; an unknown id gets them all. Match the header to answer reconnections with another mock, and serve `204 No Content`, a reply without `event_stream`, to tell clients to stop reconnecting.
- **Templates.** With `template.enabled`, or `Template()` in Go, event types, ids, comments and text data are templates. JSON data, `data_json` (`DataJSON` in Go) and `raw` are sent as they are. A templated stream's description keeps JSON data as JSON: built back, it is sent compact, with its object keys sorted. JSON with a number past int64, which a file can't hold, is written as its text, a template then.
- **Faults.** [Steps](/http/steps) and [faults](/http/faults) apply to the stream as to any body: `fault.LimitData` drops the connection after some bytes, as a flaky network does.
- **Mappers.** Response mappers read the whole body, which a stream may never end, so a mock can't have both.
