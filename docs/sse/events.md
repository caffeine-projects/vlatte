# Events

`NewEventStream(events...)` in Go, and `event_stream.events` in a mock file, list the events of a stream, sent in order. Each event's fields go on the wire as the [event stream format](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation) reads them.

::: code-group

```go [Go]
mockhttp.OK().EventStream(mockhttp.NewEventStream(
	mockhttp.NewEvent().Name("price").ID("1").DataJSON(map[string]any{"value": 10}),
	mockhttp.NewEvent().Comment("keep-alive"),
	mockhttp.NewEvent().Data("line 1\nline 2"),
	mockhttp.NewEvent().DataJSON("done"),
).Retry(3 * time.Second))
```

```yaml [Mock file]
response:
  status: 200
  event_stream:
    retry: 3s
    events:
      - {event: price, id: "1", data: {value: 10}}  # text as is, anything else as JSON
      - {comment: keep-alive}
      - {data: "line 1\nline 2"}                   # a data line per line
      - {data_json: "done"}                        # always JSON: sends "done", quotes too
```

:::

```text
retry: 3000

event: price
id: 1
data: {"value":10}

: keep-alive

data: line 1
data: line 2

data: "done"

```

## Fields

| Go | Mock file | Sends |
|---|---|---|
| `Name(name)` | `event` | The event type. Clients dispatch an event without one as a `message`. |
| `ID(id)` | `id` | The id, which clients send back as `Last-Event-ID` when they [reconnect](/sse/reconnecting). An empty id is sent too: it resets the client's last event ID. |
| `Data(text)` | `data` | The data. In a mock file, text is sent as it is, and any other value as JSON but `null`, which fails: write `""` for empty data, or `data_json: null`. |
| `DataJSON(v)` | `data_json` | The data as JSON, strings and `null` too. A value that doesn't marshal fails the mock. |
| `Retry(d)` | `retry` | How long the client waits before it reconnects, from this event on, in whole milliseconds. |
| `Comment(text)` | `comment` | A comment, sent before the fields, which clients skip. An event with only a comment is a comment. |
| `Delay(d)` | `delay` | The wait before the event: see [Timing and lifetime](/sse/timing). |
| `Raw(text)` | `raw` | Text sent as it is, in place of every field but the delay: see [Testing a client's parser](/sse/raw-events). |

`EventStream.Retry(d)`, `retry` beside `events`, sends a `retry` field first, before the events. Durations in a mock file are milliseconds or a Go duration, such as `1.5s`.

An event takes one of `data` and `data_json`. In Go, each data setter replaces what an earlier one set.

## Line breaks

Data and comments with line breaks get a line each, `data: ` or `: ` and the line, so clients read them back whole: `line 1\nline 2` above. CRLF and CR count as line breaks too.

An event type or id can't hold a line break, which would end the field there, and an id can't hold a NUL, which clients ignore: the mock fails to build. A stream whose fields are templates fails at the reply that renders one.

## Events without data

Clients dispatch an event only when it has data: one with only an id sets the client's last event ID without dispatching anything, and one with only a comment is skipped. Send an empty `data: ""` to dispatch an event with empty data.

## Templated events

With `template.enabled` in a mock file, or `Template()` on the stream in Go, the text of the events renders with each request, as [templates](/templating/) do: the event types, ids, comments and text data.

::: code-group

```go [Go]
mockhttp.Request().Method(http.MethodGet).URLPathTemplate("/users/{id}/notifications").
	Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
		mockhttp.NewEvent().Name("notification").ID("{{ .Request.PathParams.id }}-1").
			Data(`{"user": "{{ .Request.PathParams.id }}", "text": "Your order shipped"}`),
	).Template()))
```

```yaml [Mock file]
request: {method: GET, path_template: "/users/{id}/notifications"}
response:
  status: 200
  event_stream:
    events:
      - event: notification
        id: "{{ .Request.PathParams.id }}-1"
        data: '{"user": "{{ .Request.PathParams.id }}", "text": "Your order shipped"}'
  template: {enabled: true}
```

:::

JSON data, `data_json` (`DataJSON` in Go) and `raw` are sent as they are, never as templates: in a templated mock file, write data that holds a template as text, quoted as above. A template that fails to render fails the reply.

A templated stream's description, such as what the [admin API's](/admin-api/#routes) `GET /mocks/{id}` returns, keeps JSON data as JSON: built back, it is sent compact, with its object keys sorted. JSON with a number past int64, which a file can't hold, is written as text, which renders as a template.
