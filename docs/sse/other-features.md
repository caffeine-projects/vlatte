# With other features

An event stream is a reply body, so the features of HTTP mocks work with it. This page covers where a stream behaves differently, or needs a setting.

## Proxying

A [proxy reply](/http/proxying), `From(target)` in Go or `proxy` in a mock file, passes a target's event stream on event by event, as the target sends it, not once it ends: the client gets the first event while the target hasn't sent the second. The server flushes each part of an answer whose `Content-Type` is `text/event-stream`, or whose length isn't known. A proxy reply with [response mappers](/http/mocks), which read the whole body, fails on a target's stream at once.

## Recording

A [recording](/recording/) keeps a proxied stream as a plain `body`, with its headers:

```json
{
  "id": "GET-events-cc9d4ecf7d4bb412",
  "name": "GET /events",
  "request": {"method": "GET", "url": "/events"},
  "response": {
    "status": 200,
    "header": {"Cache-Control": ["no-cache"], "Content-Type": ["text/event-stream"]},
    "body": "id: 1\ndata: a\n\nid: 2\ndata: b\n\n"
  }
}
```

Played back, it sends the whole stream at once, without the target's pauses, and ends: it doesn't keep open or resume. To play it back event by event, turn the `body` into an `event_stream`, with the `delay`s and `keep_open` it needs.

## Browsers and CORS

A page's `EventSource` on another origin reads a stream only when the answer allows its origin. With `{withCredentials: true}`, it sends its cookies, and the answer must allow credentials and name the origin, not `*`:

::: code-group

```go [Go]
m := mockhttp.MustNew(mockhttp.Setup().CORS(mockhttp.CORS().
	AllowOrigin("https://app.example").
	AllowCredentials(true)))
m.MustMock(mockhttp.Getf("/events").
	Cookie("session", match.Equal("s1")).
	Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(mockhttp.NewEvent().Data("hi")).KeepOpen())))
```

```sh [CLI]
vlatte --cors-allowed-origin=https://app.example --cors-allow-credentials
```

:::

See [CORS](/http/cors) for the settings.

## The admin API and MCP

`POST /mocks` on the [admin API](/admin-api/), and the `add_mock` tool of [MCP](/mcp/tools), take a mock with an `event_stream`, as a mock file writes it, in JSON or YAML:

```sh
vlatte --admin-addr=127.0.0.1:8081
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","path":"/ticks"},"response":{"status":200,"event_stream":{"events":[{"data":"1"},{"data":"2"}]}}}' \
  http://127.0.0.1:8081/mocks
```

`GET /mocks/{id}` describes a stream built in Go as a mock file would, including built-in event delay distributions. A `vlatte.DelayFunc` cannot be serialized and makes description fail. The [mock file schema](/reference/schemas) describes `event_stream`, so editors complete it.

## Feeds per user

A [path template](/http/matching#path-templates) and a templated stream serve each user the feed the path names, and a mock with a higher priority number, `Priority(1)` or `priority: 1`, answers what the first doesn't match, a request without credentials, before any stream starts:

::: code-group

```go [Go]
m.MustMock(mockhttp.Request().Method(http.MethodGet).
	URLPathTemplate("/users/{id}/notifications").
	Header(mockhttp.HeaderAuthorization, match.HasPrefix("Bearer ")).
	Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
		mockhttp.NewEvent().Name("notification").ID("{{ .Request.PathParams.id }}-1").
			Data(`{"user": "{{ .Request.PathParams.id }}", "text": "Your order shipped"}`),
	).KeepOpen().Template())))
m.MustMock(mockhttp.Request().Method(http.MethodGet).
	URLPathTemplate("/users/{id}/notifications").
	Priority(1).
	Reply(mockhttp.Unauthorized().PlainText("sign in first")))
```

```yaml [Mock file]
request:
  method: GET
  path_template: "/users/{id}/notifications"
  header:
    authorization: [hasprefix, "Bearer "]
response:
  status: 200
  event_stream:
    keep_open: true
    events:
      - event: notification
        id: "{{ .Request.PathParams.id }}-1"
        data: '{"user": "{{ .Request.PathParams.id }}", "text": "Your order shipped"}'
  template:
    enabled: true
```

```yaml [Mock file, 401]
request:
  method: GET
  path_template: "/users/{id}/notifications"
priority: 1
response:
  status: 401
  header:
    content-type: text/plain
  body: sign in first
```

:::

The `401` makes `EventSource` give up rather than retry: see [Telling clients to stop](/sse/reconnecting#telling-clients-to-stop).
