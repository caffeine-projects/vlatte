# Webhooks and events

Once a mock has replied, it runs its `on_complete` events: each names a listener of the server, which gets the request, the mock and the event's parameters. The `webhook` package is the listener vlatte ships. In Go, `OnCompleteEvent(cfg)` adds an event to a mock, and `Callback(fn)` runs a function of yours, HTTP and gRPC mocks alike.

A webhook is a request a mock sends after it replies, as a payment API calls back to say an order was paid. The `webhook` package sends them, as an `on_complete` listener of the server.

## Registering

A mock names its listener, so the server registers one under that name first. The `vlatte` command registers a webhook as `webhook`. In Go:

```go
wh, err := webhook.New()
m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener("webhook", wh))
```

A mock that names a listener the server doesn't have fails to build, and so does a server given two listeners of one name.

A webhook sends through `webhook.WithTransport`, or else the server's `Setup().Transport`, or else a transport the server makes for webhooks, whose idle connections `m.Close()` closes; a gRPC server makes one too. A listener of your own gets the same from its event's `Kit()`: the server's `Logger()`, `Transport()`, and `Dir()`, the directory of the mock file, which relative files open from.

```yaml
request:
  method: POST
  path: /orders

response:
  status: 202

on_complete:
  - name: webhook
    parameters:
      method: POST
      url_template: '{{ .Data.callback }}/orders/{{ (fromJSON .Request.Body.String).id }}'
      header:
        content-type: application/json
      body: '{"status": "paid"}'
      delay: 500ms
```

In Go:

```go
m.MustMock(mockhttp.Postf("/orders").
	OnCompleteEvent(webhook.Options().
		URL(callback).
		Method(http.MethodPost).
		Header("Content-Type", "application/json").
		Body(`{"status": "paid"}`).
		Delay(500 * time.Millisecond).
		Build("webhook")).
	Reply(mockhttp.Accepted()))
```

`URLTemplate`, `HeaderTemplate`, `BodyTemplate` and `BodyFile` set the parameters of the same names. A multipart body whose text is templates is `Multipart(mockhttp.MultipartForm(...).Template())`.

## Parameters

| Parameter | What it sets |
|---|---|
| `url` or `url_template` | Where the request goes: an absolute `http` or `https` URL. One of them is required. |
| `method` | The method, in any case. Empty means `GET`. |
| `header`, `header_template` | Headers by name, each a string, or a list of strings to send the header once per value, in order. Names ignore case, so a name is set once. |
| `body`, `body_base64`, `body_template`, `body_file`, `multipart`, `multipart_template` | The body, one of them at most. A body that isn't UTF-8 text goes in `body_base64`, as standard base64, whose line breaks are ignored; a `Body` set in Go that isn't UTF-8 describes there. Multipart bodies are as in [replies](/http/forms-multipart). |
| `delay` | How long to wait before sending, once the mock replied. The response waits for it too, unless the webhook runs after the response. |
| `timeout` | How long the request may take, from sending it to its answer. Without it, the listener's timeout, or else 10s. |

Templates render with the data of the request that completed the mock: see [Templates](/templating/). A mistake, such as a missing URL, two bodies or a template that doesn't compile, fails the mock when it is built.

## When they run

Webhooks are sent once the mock has replied, each on its own, and the response ends when they all have: a slow or failing webhook holds up no other, and a test checks what they sent right after the call:

```go
// callbacks is a mock server standing for the client the webhook calls back.
res, err := http.Post(m.URL("/orders"), "application/json", strings.NewReader(`{"id": 42}`))
// ...
callbacks.AssertCalled(t)
```

A webhook's `delay` counts from the reply, so it holds the response too. A reply that streams its parts, or a body over a few KB, sends its status and headers before its webhooks are done, so the call returns early; the body still ends after them, unless the mock sets `Content-Length` itself. Read the body to its end, as `io.ReadAll` does, before checking what they sent. `m.Close()` cancels the webhooks still waiting, for their delay or for their target.

## After the response

`after_response: true` on an `on_complete` entry, beside `name` and `parameters`, sends the response without waiting for the event, for an app that needs its callback once it has its answer. In Go, set `AfterResponse` on the `vlatte.EventConfig` that `Build` returns. The event starts as the response goes out, so give it a `delay` when the client must read its answer first, and have the test wait for the target to get the webhook, not for the call. `m.Close()` cancels these events too, and waits for them. A failure is logged as any other, but the journal entry of the request, recorded by then, doesn't tell it.

```yaml
on_complete:
  - name: webhook
    after_response: true
    parameters:
      url: http://localhost:8081/orders
      delay: 100ms
```

## Failures

A webhook fails when its target doesn't answer in time, or answers with a status outside 2xx: a redirect isn't followed. The server logs the failure as a warning, and the request's [journal](/more/journal) entry, if the server keeps one, tells it in its `Message`. The timeout is 10 seconds: a webhook's `timeout` parameter (`Options().Timeout(d)` in Go) changes it for that webhook, and `webhook.WithTimeout` for every webhook its listener sends, where 0 means 10 seconds, unlike `proxy.timeout`. The CLI's `webhook.timeout` setting is the `WithTimeout` of its listener, for HTTP and gRPC mocks. A webhook reads up to 256 KiB of the answer, so its connection carries the next one. A response waits for a webhook at most its delay plus the timeout.

## Logs

Webhooks log each request and its answer with the server's logger, so `Setup().Silent()` silences them too. `webhook.WithLogger` gives them a logger of their own.

## In gRPC

gRPC mocks run `on_complete` events the same way, with `OnCompleteEvent` in Go and `on_complete` in a [gRPC mock file](/grpc/mock-files). Register the listener on the gRPC server:

```go
wh, err := webhook.New()
m := mockgrpc.MustNew(mockgrpc.Setup().OnCompleteListener("webhook", wh))
```

The webhook's templates render with the data of the call: see [Templates in gRPC](/templating/#in-grpc).

