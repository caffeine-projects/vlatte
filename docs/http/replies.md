# Replies

A mock's reply says how it answers. In Go, `Reply(...)` takes a reply; in a [mock file](/http/mock-files), `response` describes one, and `response_sequence`, `response_random`, `proxy` and `proxy_all` the others.

## Status, headers and body

A `*mockhttp.Reply` starts from its status: `OK()`, `Created()`, `Accepted()`, `NoContent()`, `BadRequest()`, `Unauthorized()`, `Forbidden()`, `NotFound()`, `InternalServerError()` and the others, `Status(code)` for any code, and `NewReply()`.

```go
m.MustMock(mockhttp.Getf("/users/1").Reply(mockhttp.OK().
	Header("Cache-Control", "no-store").
	JSON(map[string]any{"id": 1, "name": "ann"})))
```

```yaml
request: {method: GET, path: /users/1}
response:
  status: 200
  header:
    cache-control: no-store
  body: {id: 1, name: ann}
```

| Go | Mock file | Sets |
|---|---|---|
| `Status(code)` | `status` | The status. |
| `Header(k, v)`, `HeaderArr(k, vs...)` | `header` | Headers. A value can be a list. |
| `HeaderTemplate(k, v)`, `HeaderArrTemplate` | `header_template` | Headers rendered from the request: see [Templates](/templating/). |
| `Trailer(k, v)`, `TrailerArr(k, vs...)` | `trailer` | Trailers. |
| `Cookie(c)`, `ExpireCookie(c)` | | Cookies, and cookies that expire one the client holds. |
| `ContentType(mime)` | `header: {content-type: ...}` | The `Content-Type`. |
| `PlainText(text)`, `JSON(v)` | | A `text/plain` or `application/json` body. |
| `BodyText(s)`, `Body(b)`, `BodyJSON(v)`, `BodyReader(r)` | `body`, `body_base64` | The body. A mock file's `body` is text as is, and any other value as JSON. |
| `BodyFile(name)`, `BodyFileWithTemplate(name)` | `body_file` | A body read from a file, relative to the mock file. |
| `BodyTemplate(text)` | `body` with `template.enabled` | A body rendered from the request. |
| `Problem(p)` | | An [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem document, built with `NewProblem()`. |
| `Multipart(mp)` | `multipart` | A multipart body: see [Forms and multipart](/http/forms-multipart#multipart-replies). |
| `EventStream(es)` | `event_stream` | A `text/event-stream` body, sent event by event: see [SSE (Server-sent Events)](/sse/). |
| `Gzip()` | `encoding: gzip` | A gzip-encoded body. |

A body that isn't UTF-8 text, such as an image or text in another encoding, goes in `body_base64`, as standard base64 (`body_base64: //4A`), whose line breaks are ignored, or in a `body_file`. A reply takes one of `body`, `body_base64`, `body_file`, `multipart` and `event_stream` at most.

```go
m.MustMock(mockhttp.Postf("/orders").Reply(mockhttp.UnprocessableEntity().Problem(mockhttp.NewProblem().
	Title("Out of stock").
	Detail("item 42 is out of stock").
	Extension("item", 42))))
```

## Sequences

`Seq(replies...)` answers each request with the next reply, in order, then the reply of `OnSequenceEnded`. A request past the end without one fails, with `VLatte-Error: reply`. Each mock built with a sequence runs it on its own, from the first reply.

```go
m.MustMock(mockhttp.Getf("/jobs/1").Reply(mockhttp.Seq(
	mockhttp.OK().JSON(map[string]any{"status": "running"}),
	mockhttp.OK().JSON(map[string]any{"status": "running"}),
).OnSequenceEnded(mockhttp.OK().JSON(map[string]any{"status": "done"}))))
```

```yaml
request: {method: GET, path: /jobs/1}
response_sequence:
  responses:
    - {status: 200, body: {status: running}}
    - {status: 200, body: {status: running}}
  after_ended: {status: 200, body: {status: done}}
```

## Random replies

`Rand(replies...)` answers each request with one of its replies at random. `RandWithSeed(seed, replies...)` picks in an order the seed repeats, and `RandWith(r, replies...)` with a `*rand.Rand` of yours. In a mock file, `response_random` takes `responses` and `seed`.

```yaml
request: {method: GET, path: /flaky}
response_random:
  seed: 42
  responses:
    - {status: 200}
    - {status: 503}
```

## Proxy replies

`From(target)` answers with what another server answers, `proxy` in a mock file. See [Proxying](/http/proxying).

## Replies in Go only

Mock files can't hold these, so the admin API can't show, save or store a mock that uses them.

| Reply | Answers |
|---|---|
| `Echo()` | The request back, as it came. `Log()` logs each. |
| `Func(fn)` | What `fn` returns, a `*mockhttp.Response`, or what it writes to the `http.ResponseWriter` when it returns `nil`. |
| `Handler(h)` | What an `http.HandlerFunc` writes. |
| `Shared(replier)` | The same `Replier` for every mock built with it, which they never close. |

```go
m.MustMock(mockhttp.Postf("/echo").Reply(mockhttp.Echo()))

m.MustMock(mockhttp.Getf("/now").Reply(mockhttp.Func(func(w http.ResponseWriter, r *mockhttp.RequestValues) (*mockhttp.Response, error) {
	return &mockhttp.Response{StatusCode: http.StatusOK, Body: []byte(time.Now().Format(time.RFC3339))}, nil
})))
```

The context of the request of a `Func` or `Handler` reply also ends when a [pipe](/http/pipes) of the mock cuts the answer.
