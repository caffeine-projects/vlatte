# vlatte

Mock HTTP and gRPC servers for Go tests, and a CLI that serves HTTP mocks from files.

## Install

Requires Go 1.27 or later.

```sh
go get github.com/caffeine-projects/vlatte
```

The CLI:

```sh
go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest
```

## Mock HTTP calls in tests

```go
package hello_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestHello(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK().PlainText("hello world")))

	res, err := http.Get(m.URL("/hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q", body)
	}

	scoped.AssertCalled(t)
}
```

`mockhttp.New` does the same as `MustNew` but returns an error instead of panicking. Pass `mockhttp.Setup()` to either one to configure the server: its address, TLS, CORS, proxying or recording, for example. The `mockgrpc` package offers the same for gRPC services.

## Packages

| Package | What it's for |
|---|---|
| `mockhttp` | HTTP mock servers |
| `mockgrpc` | gRPC mock servers |
| `match` | Request matchers, such as `match.Equal`, `match.JSONPath` and `match.All` |
| `webhook` | Calling a URL after a mock is served |
| `recipe/<protocol>/<recipe>` | Recipes that prepare a server, such as `recipe/http/proxyall` |
| `vlatte` | The types every mock server shares, and the Go template engine |

A recipe prepares a server through `Prep`. This one forwards every request that no mock matches, with the options of the proxy reply it takes:

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
```

## Mock files

Mocks can also live in YAML or JSON files (`.json`, `.yaml` or `.yml`).
- **On start,** the server loads the files matching `testdata/_mocks/*mock.yaml` and `testdata/_mocks/*mock.yml`, and the [recordings](#recording-and-playback) in `testdata/_mocks_recorded`, in name order. Change the patterns with `Setup().MockFilePatterns(...)`.
- **To load one file,** use `m.MustMock(mockhttp.FromFile(path))`.
- **`m.Reload()`** replaces the mocks of the files with what they hold now, in one step. When a file has problems, it returns them and the mocks stay as they were.

```yaml
# testdata/_mocks/hello_mock.yaml
id: hello            # optional: names the mock, as in the admin API's /mocks/hello
request:
  method: GET
  path: /hello

response:
  status: 200
  header:
    content-type: text/plain
  body_file: hello.txt   # relative to this file
```

[`mockhttp/schema/http.schema.json`](mockhttp/schema/http.schema.json) describes the format, so editors can validate mock files.

**Types.** Each key takes one type, as the schema says. Write text in quotes where a string goes, as in `name: "123"`, and numbers bare, as in `priority: 10`. Bools read the way Go's `strconv.ParseBool` reads them, so `true`, `"true"`, `t` and `1` are all true. A value of the wrong type is an error that says how to write it.

**Durations.** `delay`, `proxy.timeout` and the `delay` of a [webhook](#webhooks) take whole milliseconds (`1500`) or a Go duration (`1.5s`). `"1500"` in quotes is neither.

**Matching.** Most of a request is text: its path, query, headers and form values. A matcher reads that text as the type it compares with:
- `[equal, 10]` and `[isIn, [10, 20]]` match `?page=10`;
- `[gt, 18]` matches `?age=30`;
- `[equal, true]` matches `?debug=1`.

No other reading happens: `[equal, 10]` doesn't match `010`. The [match package docs](match/doc.go) state the rule.

**Keys.** Keys are kept as written, so a query name like `userId` matches `userId` only. Header names, and methods written as text, ignore case, so `method: get, post` matches `POST`; cookie names don't.

**URLs.** `url: /items?page=2` matches that path and query exactly: the same parameters, in any order, each with its values in order, so `/items` and `/items?page=2&sort=asc` don't match. Path segments compare decoded, so `%41` is `A`, but `%2F` isn't `/`. A relative `url` with a `*` in its path is an error: match a path with a glob through `path: /items/*`, and parameters one by one through `query`. For a literal `*`, write `%2A`. An absolute `url`, such as `https://api.example.com/items`, compares as text ignoring case, with the server's URL, or the one a request names in absolute form, as the forward proxy gets it, or intercepted, without its default port. In Go, `RequestURI("/items?page=2")` matches as a relative `url` does.

**Path templates.** `path_template: /users/{id}/orders/{orderId}` matches paths of its shape: as many segments, the literal ones the same once decoded, and a non-empty one for each parameter, so `/users/7/orders` and `/users/7/orders/9/` don't match. A parameter is a whole segment; write a literal brace as `%7B` or `%7D`. `path_params` matches the parameters by name, decoded, and templates read them as `.Request.PathParams.id`, or `index .Request.PathParams "order-id"` for a name that isn't a Go identifier. `url`, `url_match` or `path` can match the request too. In Go, `URLPathTemplate` and `PathParam`.

```yaml
request:
  method: GET
  path_template: /users/{id}
  path_params:
    id: [regex, '^[0-9]+$']
response:
  body: 'user {{ .Request.PathParams.id }}'
  template:
    enabled: true
```

**Many values.** `query`, `header` and `form` read the first value of a name. `queries`, `headers` and `forms` (`Queries`, `Headers` and `FormFields` in Go) read every value, as a list, so they take matcher lists, such as `[contains, a]`, `[itemsMatch, [a, b]]` or `[len, 2]`: they refuse text, which never equals a list. A name the request doesn't have is an empty list, which `[not, present]` matches, and a header sent empty isn't. A header line such as `a, b` is one value.

**More of the request.** `cookie` matches cookies by name, as `Cookie` does; a cookie the request doesn't have is null, which no text matches. `basic_auth: {username: jeff, password: s3cret}` matches Basic credentials, as `BasicAuth` does, and `BasicAuthMatches` takes matchers; without credentials, the username and password are null, which no text matches. `host` matches the host the request names, without its port, and text ignores case: `Host` in Go. `client_ip` matches the client's address, as the server's connection has it, not `X-Forwarded-For`: `ClientIP` in Go.

**Proxy replies.** A `proxy` reply (`From` in Go) answers with what its target answers: see [Proxying](#proxying).

**Templates.** A mock file is plain JSON or YAML. Only the settings that are templates, such as a templated body, render: see [Templates](#templates).

**Errors.** A file with problems isn't loaded. The error lists every problem with its key and a fix:

```
mock: 2 problems
  file testdata/_mocks/users_mock.yaml: unknown_key: response.stauts is not a known setting; did you mean response.status?
  file testdata/_mocks/users_mock.yaml: conflict: proxy is set with response; keep one of them
```

## Templates

Replies can render parts of the response from the request: header values, the body, and the name or content of a body file. So can multipart parts and webhooks.

```yaml
request:
  method: POST
  path: /users

response:
  status: 201
  header_template:
    location: '/users/{{ .Request.Header.Get "X-User-Id" }}'
  body: '{"id": {{ uuid | toJson }}, "name": {{ (fromJson .Request.Body.String).name | toJson }}}'
  template:
    enabled: true
```

In Go:

```go
m.MustMock(mockhttp.Postf("/users").Reply(mockhttp.Created().
	HeaderTemplate("Location", `/users/{{ .Request.Header.Get "X-User-Id" }}`).
	BodyTemplate(`{"id": {{ uuid | toJson }}}`)))
```

**What is a template.** Nothing else renders: `{{` anywhere else is text.
- `header_template` values (`HeaderTemplate`).
- With `template.enabled`: a text `body` (`BodyTemplate`), the content of a `body_file` (`BodyFileWithTemplate`), and the text of `multipart` parts (`Template()`).
- `body_file` names, always. The content of the file is written as it is unless `template.enabled` is set.
- Webhook parameters: `url_template`, `header_template`, `body_template` and `multipart_template`.

**Data.** Templates read:

| Field | What it holds |
|---|---|
| `.Request.Method`, `.Request.URL`, `.Request.URLPathSegments`, `.Request.Header`, `.Request.ID` | The request. `index .Request.URLPathSegments 0` is the first segment of the path. |
| `.Request.PathParams.<name>` | The parameters of the mock's [path template](#mock-files), decoded. |
| `.Request.Body.String`, `.Request.Body.Content`, `.Request.HasBody` | The body. JSON bodies are text: read their fields with `fromJson`. |
| `.Request.Cookie "name"`, `.Request.PostForm.Get "f"`, `.Request.FormFile "f"` | Cookies and forms. |
| `.Server.URL` | The server's base URL. |
| `.Parameters.Get "key"`, `.Parameters.Has "key"` | The server's parameters. |
| `.Data` | The map set with `m.SetData`. |
| `.Provided.<key>` | What `Setup().TemplateDataProviders(...)` provide. |
| `.Extra` | The reply's `template.data`, or `SetTemplateData` in Go. Webhooks don't have it. |

**Functions.** Besides those of Go templates, such as `index`, `printf`, `html` and `urlquery`: `toJson`, `fromJson`, `uuid`, `now`, `base64Encode` and `base64Decode`. Add your own to the engine:

```go
engine := vlatte.MustNewGoTemplateEngine(vlatte.WithTemplateFuncs(map[string]any{"upper": strings.ToUpper}))
m := mockhttp.MustNew(mockhttp.Setup().TemplateEngine(engine))
```

**Missing keys** are errors. `{{ .Extra.nmae }}` fails the request and names the key, instead of writing `<no value>`, and so does `.Parameters.Get` of a parameter that isn't set. Read an optional key with `index`, as in `{{ with index .Extra "nick" }}{{ . }}{{ end }}`, and an optional parameter with `.Parameters.Has` first. `vlatte.WithMissingKey` changes the rule for map keys.

**Escaping.** Templates are Go's `text/template`, so a body can be any format and nothing is escaped for you. Write JSON values with `toJson`, and use `html` or `urlquery` where they fit.

**Errors.** Templates compile when the mock is built, so one that doesn't compile fails the mock. A body file whose content is a template compiles when a request reads it, since its name can depend on the request. A template that fails at a request answers as a [failure](#failures): the error status, `VLatte-Error: reply`, and a body that names the mock and the error.

**File names.** A body file name rendered from the request stays inside the directory written before the template: `files/{{ .Request.URL.Query.Get "f" }}` opens files in `files` only, and refuses `../` or an absolute path from the request.

**Header values** are written without the spaces and line breaks around them, as `net/http` writes headers, so a YAML block scalar works. A line break inside a rendered value fails the reply, or the webhook, so the request can't add a header.

**Webhooks** compile their templates when the mock is built, and render them each time they run, with the data of the request that completed the mock.

**Other engines.** A server compiles all its templates with one `vlatte.TemplateEngine`. vlatte ships `vlatte.GoTemplateEngine`; another engine, such as one for Handlebars, plugs in by implementing two methods:

```go
type TemplateEngine interface {
	Compile(name, text string) (Template, error)
}

type Template interface {
	Execute(w io.Writer, data any) error // safe for concurrent use
}
```

Pass it with `Setup().TemplateEngine(e)`. Templates then use its syntax everywhere, mock files included, as in `{{Request.Method}}` for Handlebars. [`template_handlebars_test.go`](mockhttp/e2e/template_handlebars_test.go) wraps [raymond](https://github.com/mailgun/raymond) in a few lines. Other engines read the same fields, maps and methods without arguments; methods with arguments, such as `.Parameters.Get "key"`, are Go template syntax.

## Forms and multipart

Go's `net/http` parses request bodies: `ParseForm` reads urlencoded forms and `ParseMultipartForm` reads `multipart/form-data`. Bodies are parsed by their media type, in any case: `text/plain` and `application/json` as text, and the two form types as forms.

**Fields.** `FormField` (file key `form`) reads a field of either encoding, as `http.Request.PostFormValue` does: its first value, and never the query. `FormFields` (file key `forms`) reads every value.

**Files.** `FormFile` (file key `form_file`) matches the files of a field with a pattern. Some file of the field must meet every condition of the pattern, or every file with `Every()` (`every: true`). With several patterns for one field, each must be met by some file of the field, and one file may meet several.

```go
m.MustMock(mockhttp.Postf("/upload").
	FormField("title", match.Equal("Holiday")).
	FormFile("avatar", mockhttp.FileWith().
		Filename(match.HasSuffix(".png")).
		ContentType(match.Equal("image/png"))).
	Reply(mockhttp.Created()))
```

```yaml
request:
  method: POST
  path: /upload
  form:
    title: Holiday
  form_file:
    avatar:
      filename: [hasSuffix, .png]
      content_type: image/png   # the media type, in lower case, without parameters
      body_file: avatar.png     # the file's bytes; or body: text or a matcher list
```

Go's rules apply:
- a part without a field name isn't read;
- a file's name is its base: `dir/a.png` arrives as `a.png`;
- a text field keeps no headers;
- a form holds at most 1000 parts, which `GODEBUG=multipartmaxparts` changes.

A form body that doesn't parse, or that was cut short, has no fields at all, and the server logs a warning. Other multipart requests, such as `multipart/mixed`, aren't parsed: match them with `Body`.

**Multipart replies.** `Reply.Multipart` writes a body of any multipart subtype with Go's `multipart.Writer`. The body sets the `Content-Type`, with its boundary. When none is set, a random boundary is drawn once per mock, so every response is the same bytes.

```go
m.MustMock(mockhttp.Getf("/report").Reply(mockhttp.OK().Multipart(mockhttp.NewMultipart("related",
	mockhttp.NewPart().Header("Content-Id", "<root>").BodyJSON(map[string]any{"id": 1}),
	mockhttp.NewPart().Name("report").Filename("report.pdf").BodyFile("testdata/report.pdf"),
).Param("start", "<root>"))))
```

```yaml
response:
  status: 200
  multipart:
    type: related              # the subtype: form-data, mixed, related, x-mixed-replace…
    params: {start: "<root>"}
    parts:
      - header: {content-id: "<root>"}
        body: {id: 1}          # text as is, anything else as JSON
      - name: report
        filename: report.pdf
        body_file: report.pdf  # relative to the mock file
```

- **Defaults.** A part with a name or a filename gets its `Content-Disposition`. A JSON body defaults to `application/json`, and a part with a filename to `application/octet-stream`.
- **Templates.** With `template.enabled`, or `Template()` in Go, the text of the parts is a template: names, filenames, content types, header values, text bodies and body file names. Byte and JSON bodies, and the content of body files, are written as they are.
- **Encode.** `Multipart.Encode` writes the same bodies for requests in tests.

**Streaming.** A part with a `delay` waits that long before it is written, and everything before it is flushed first. A client therefore reads each part as it comes, as `multipart/x-mixed-replace` camera feeds need.

```yaml
response:
  status: 200
  multipart:
    type: x-mixed-replace
    parts:
      - {content_type: image/jpeg, body_file: frame1.jpg}
      - {content_type: image/jpeg, body_file: frame2.jpg, delay: 500ms}
```

**Webhooks** send multipart bodies too:
- the `multipart` parameter takes the same object, and `multipart_template` makes its text a template;
- Go uses `webhook.Options().Multipart(mockhttp.MultipartForm(...))`;
- a webhook sends its body at once, so its parts can't have delays;
- relative body files open from the mock file's directory, as in replies: a whole `body_file` and a part's alike.

**Templates** read forms: `{{ .Request.PostForm.Get "title" }}` and `{{ with .Request.FormFile "avatar" }}{{ .Filename }}{{ end }}`.

**Recording** matches a recorded upload by all its text fields, each with its values in order, and its files by `form_file` patterns, so it replays whatever boundary the next client picks. Files that aren't UTF-8 text go to files next to the recording. An upload with another field doesn't match; one with more files still does. See [Recording and playback](#recording-and-playback).

## Webhooks

A webhook is a request a mock sends after it replies, as a payment API calls back to say an order was paid. The `webhook` package sends them, as an `on_complete` listener of the server.

**Registering.** A mock names its listener, so the server registers one under that name first. The `vlatte` command registers a webhook as `webhook`. In Go:

```go
wh, err := webhook.New()
m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener("webhook", wh))
```

A mock that names a listener the server doesn't have fails to build, and so does a server given two listeners of one name.

A webhook sends through `webhook.WithTransport`, or else the server's `Setup().Transport`, or else a transport the server makes for webhooks, whose idle connections `m.Close()` closes.

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
      url_template: '{{ .Data.callback }}/orders/{{ (fromJson .Request.Body.String).id }}'
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

**Parameters.**

| Parameter | What it sets |
|---|---|
| `url` or `url_template` | Where the request goes: an absolute `http` or `https` URL. One of them is required. |
| `method` | The method, in any case. Empty means `GET`. |
| `header`, `header_template` | Headers by name, each a string. Names ignore case, so a name is set once. |
| `body`, `body_template`, `body_file`, `multipart`, `multipart_template` | The body, one of them at most. Multipart bodies are as in [replies](#forms-and-multipart). |
| `delay` | How long to wait before sending, once the mock replied. The response waits for it too. |

Templates render with the data of the request that completed the mock: see [Templates](#templates). A mistake, such as a missing URL, two bodies or a template that doesn't compile, fails the mock when it is built.

**When they run.** Webhooks are sent once the mock has replied, each on its own, and the response ends when they all have: a slow or failing webhook holds up no other, and a test checks what they sent right after the call:

```go
// callbacks is a mock server standing for the client the webhook calls back.
res, err := http.Post(m.URL("/orders"), "application/json", strings.NewReader(`{"id": 42}`))
// ...
callbacks.AssertCalled(t)
```

A webhook's `delay` counts from the reply, so it holds the response too. A reply that streams its parts, or a body over a few KB, sends its status and headers before its webhooks are done, so the call returns early; the body still ends after them, unless the mock sets `Content-Length` itself. Read the body to its end, as `io.ReadAll` does, before checking what they sent. `m.Close()` cancels the webhooks still waiting, for their delay or for their target.

**Failures.** A webhook fails when its target doesn't answer in time, or answers with a status outside 2xx: a redirect isn't followed. The server logs the failure as a warning. The timeout is 10 seconds: `webhook.WithTimeout` changes it for one webhook, and the server's `webhook.timeout` setting (`Config.WebhookTimeout`) for webhooks without one, where 0 means 10 seconds, unlike `proxy.timeout`. A webhook reads up to 256 KiB of the answer, so its connection carries the next one. A response waits for a webhook at most its delay plus the timeout.

**Logs.** Webhooks log each request and its answer with the server's logger, so `Setup().Silent()` silences them too. `webhook.WithLogger` gives them a logger of their own.

## Proxying

vlatte sends requests on to real servers in three ways:

- a **proxy reply**: a mock that answers with what its target answers, `From(target)` in Go and the `proxy` key in mock files;
- the **forward proxy**: clients use the server as their HTTP proxy, `Setup().Proxy()` in Go and the `proxy` setting, and what no mock matches goes to its host;
- the **recording target**: see [Recording and playback](#recording-and-playback).

The three share one engine, and answer alike:

- The body streams as the upstream sends it, trailers included. A body of unknown length, or an event stream, reaches the client as it comes.
- The hop-by-hop headers of either connection, such as `Connection`, `Keep-Alive` and `Proxy-Authorization`, don't go on. A request's `Te: trailers` does.
- The upstream's `VLatte-*` headers, such as the debug headers of an upstream that is a vlatte server too, don't reach the client: it gets the proxy's.
- Compressed bodies go on as they came, and a redirect goes back to the client: the proxy doesn't follow it.
- A request that asks to switch protocols, such as a WebSocket handshake (`Connection: Upgrade`, `Upgrade: websocket`), asks the upstream too. Once the upstream switches, the client gets its `101`, and the two connections are piped until both sides end, or the server closes, while the mock's callbacks and events run. A mock counts the call, and the log shows it, when the switch is done. A mock with steps or response mappers doesn't ask the upstream to switch, as they can't apply to a connection: the upstream answers as to any request. `h2c`, which asks for HTTP/2 on the client's own connection, isn't asked on. An upstream that switches to a protocol the client didn't offer fails the call. A switch needs HTTP/1.1.
- The timeout bounds the wait for the response headers, connecting and sending the request included: 30 seconds by default, and 0 waits forever. The body has no time limit.
- A call that fails gets the server's answer to a failure, `ErrorStatusCode` with `VLatte-Error: reply`, unless the client left: then the log warns. Errors and logs show the upstream's URL without its password, and with its query values masked: `?api_key=xxxxx`. An upstream body that breaks off cuts the client's connection, so the client doesn't take part of it for all of it.
- A request the proxy sends on carries the server's ID in `VLatte-Via`. A request that passes the same server 10 times, as in a loop, such as through a proxy reply whose target is the server itself, fails as a proxy loop rather than being sent on forever. A route may pass a server more than once, such as through a gateway that sends it back.
- What the proxy keeps of a body, for the log, callbacks and events, is at most 1 MiB, and only when one of them reads it.
- A request body the server read whole, shorter than `MaxBodyParsingLimit` and without `NoBodyParsing`, can be sent again: the transport resends a replayable request, such as one with an `Idempotency-Key`, on a new connection when the one it reused turned out closed.

**Proxy replies.** The target's path and query come before the request's: with `target: http://api.local/v1?tenant=a`, a request to `/users?page=2` goes to `http://api.local/v1/users?tenant=a&page=2`. `trim_prefix` and `trim_suffix` cut the request path first: `trim_prefix` cuts whole segments, so `/api` cuts `/api/users` to `/users` and leaves `/apiary` as it is, and `trim_suffix` cuts text, such as an extension: `.json` cuts `/users.json` to `/users`. When the target has a path, a request path with a `.` or `..` segment, escaped or not, fails: it could leave that path. `request_header` sets headers of the request the target gets, in place of the client's values of the same name, and `remove_request_headers` leaves request headers out; `header` sets headers of the response, in place of the target's. `x_forwarded: true` (`XForwarded()` in Go) tells the target about the client: its address in `X-Forwarded-For`, after those of the proxies before, the host it asked for in `X-Forwarded-Host`, and its scheme in `X-Forwarded-Proto`. `preserve_host: true` (`PreserveHost()`) sends the target the `Host` the client asked for, in place of the target's own. `timeout` is the timeout. A mock's `delay` comes before the call: a client that leaves during it gets nothing, and the target isn't called. A mock with response mappers reads the whole body first, so they see it in `Body`: up to 1 MiB, the most a proxy keeps of a body. A larger body fails the reply, and so does an event stream (`text/event-stream`) at once, as it doesn't end; a stream of another type that never ends waits for the client to leave. A mock's steps can change the body's length: the target's `Content-Length` doesn't go on. A mock's description, which the admin API serves, keeps the target as it was set, credentials included, so it loads back as the same mock.

The target's TLS certificate isn't verified unless `ssl_verify: true`, so a target with a self-signed certificate answers. Set it for a target that gets credentials, such as `https://user:pass@api.local`, whose user info goes as Basic auth: over `http`, it goes in cleartext. `ssl_verify` also decides whether the certificate of an `https` proxy on the way, from `HTTPS_PROXY`, is verified.

**Proxy all.** `ProxyAll(From(target))` in Go mocks what no other mock matches with a proxy reply, as WireMock's `--proxy-all` does: it matches any method and path, and comes last, at priority `math.MaxInt`. It leaves alone `CONNECT`s, and, on a server that is a forward proxy, the requests in absolute form, which the proxy sends on to their hosts. In a mock file, `proxy_all` takes the keys of `proxy` and makes the same mock, without `request`; a `ProxyAll` mock's description loads back as one. The `proxyall` recipe mocks it. The `proxy_all` setting does the same without a mock, so assertions don't count it, and `Clean` and `Reload` leave it: `vlatte --proxy-all-target=https://api.example.com`. It takes the keys of a mock file's `proxy` but `request_header` and `header`, which flags and variables can't give.

**Forward proxy.** A client sends its requests to the server in absolute form, `GET http://api.local/users`, and opens a tunnel with `CONNECT` for an `https` URL. A mock that matches the request answers it; mocks match the path and ignore the host, unless they match it, with `host` or an absolute `url`. A request no mock matches goes to its host. A request in origin form, `GET /users`, names no host: no mock matching it, it gets the no-match answer. `proxy.timeout` is the timeout, and `proxy.ssl_verify` verifies the certificates of the hosts, and of an `https` proxy on the way, `proxy.via` or the one of `HTTPS_PROXY`: none is verified by default. `proxy.via` sends the requests through another proxy, an `http`, `https`, `socks5` or `socks5h` URL whose user info goes as its credentials: a client's `Proxy-Authorization` is for vlatte, and doesn't go on.

A `CONNECT` no mock matches opens a tunnel to its host, through `proxy.via` or the proxy of the environment, if any, within `proxy.timeout`, and the log shows that it opened. What goes through a tunnel, such as a TLS connection, isn't read: mocks don't see it, unless the proxy intercepts it. Each side of a tunnel ends what it sends on its own, and the tunnel closes once both did, or once the server closes. A tunnel needs HTTP/1.1, and a response writer that can hijack its connection: a middleware's writer must unwrap, with `Unwrap() http.ResponseWriter`, to the server's.

**HTTPS interception.** With `proxy.intercept` (`ProxyConfig{Intercept: true}` in Go), the proxy serves the TLS of a `CONNECT` no mock matches itself, as WireMock's HTTPS browser proxying does. It answers with a certificate for the host the `CONNECT` names, or the name the client asks for, signed by its CA, which clients must trust. The requests within are then the server's own, for `https://` that host, which `scheme` and an absolute `url` see: mocks answer them, recordings record them, the log shows them, and the proxy sends the rest on to the host. A connection that doesn't start with TLS goes to the host through a tunnel, as does one whose client offers only `h2` by ALPN, such as a gRPC client, and one whose client sends nothing for 2 seconds, waiting for a host that speaks first, such as an SSH server: a host the proxy can't reach then closes the connection, as the `200` went out already. `proxy.ca_cert` and `proxy.ca_key` name the CA's files, which the settings need: a CA's certificate, or an intermediate CA's, followed by its chain, which the proxy sends with each host's. In Go, `ProxyConfig.CA` sets it, and without one, the server makes a CA of its own, valid for a year, which `m.ProxyCA()` returns for clients to trust:

```go
m := mockhttp.MustNew(mockhttp.Setup().Proxy(&mockhttp.ProxyConfig{Intercept: true}))
pool := x509.NewCertPool()
pool.AddCert(m.ProxyCA())
client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}}}
```

An intercepted connection speaks HTTP/1.1.

**Transports.** In Go, `From(target).Transport(rt)` sends a proxy reply through a transport of yours, such as one with a client certificate, and `Setup().Transport(rt)` sets one for every proxy reply, the recording target, the forward proxy and webhooks. A transport of yours replaces the one vlatte makes, and decides on its own how it verifies certificates and which proxy it goes through: `ssl_verify`, `proxy.via`, `proxy.ssl_verify` and `record.skip_ssl_verify` fail with it, rather than being ignored. The timeouts apply to any transport.

The transports vlatte makes go through the proxy of `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, which leaves out `localhost`, unless `proxy.via` sets one. Docker can set these variables in containers from `~/.docker/config.json`, and so send the hosts of a compose network, such as `http://api:8080`, to that proxy: add them to `NO_PROXY`. A variable that names the vlatte server itself sends the requests it proxies back to it, until they fail as a proxy loop: set `NO_PROXY=*` for its process, or `proxy.via`.

## Recording and playback

A server records what it proxies as mock files, then answers the same requests the same way, without the service.

```go
m := mockhttp.MustNew()
m.MustStart()
t.Cleanup(m.Close)

err := m.StartRecording(
	mockhttp.RecordDir("testdata/recorded"),
	mockhttp.RecordTarget("https://api.example.com"),
	mockhttp.RecordPaths("/v1/*"),
)
// ... requests to m.URL("/v1/...") reach the target, and are recorded ...

res, err := m.StopRecording(ctx) // writes the mock files, then serves them
```

**What is recorded.**
- With a target, the requests the recording takes go to the target before any mock, and its answers are recorded. The others go to the mocks, as usual.
- Without one, the recording takes what the server proxies: the forward proxy (`Setup().Proxy()`) and proxy replies (`From`, the `proxy` key). A mock's own answer is recorded only with `RecordIncludeMocked(true)`.
- Only an answer the client got whole is recorded. The failures vlatte answers, such as a target it can't reach, aren't.

**Filters.** `RecordMethods`, in any case, and `RecordPaths` choose the requests the recording takes. A path is a glob whose `*` matches `/` too, so `/v1/*` takes `/v1/a/b`. `RecordFilter` takes any request pattern, made as a mock is:

```go
mockhttp.RecordFilter(mockhttp.Request().Header("X-Record", match.Equal("yes")))
```

A filter that fails takes nothing: the request is served as usual, and the failure is logged.

**Playback.**
- `StopRecording` waits until every call it took is written, then serves the recordings: the mock files load again, with the recording's directory, so the hits of file mocks count from 0. When `ctx` ends first, it returns ctx's error, and the recording keeps writing: another `StopRecording` waits for it again, and a `StartRecording` once it wrote everything ends it. Either serves it. A `StopRecording` that `Close` cuts short says the recordings aren't served.
- A server started later serves them too. The default mock file patterns include `testdata/_mocks_recorded`, and a server serves the recordings of its `record.dir`.
- A server serves the recordings of the last recording `StopRecording` ended, not those of earlier ones in other directories. The same request recorded into two directories a server serves has one ID twice: `StopRecording` reports it, and the server serves what it served before.
- A mock that proxies every request needs a priority number above 0 to let the recordings answer first: `AnyMethod().Priority(10).Reply(From(target))`.
- A recording answers its request before any proxy, so a later recording without a target doesn't record that request again: it records what is new. A recording with a target records every request it takes.

**What a recording matches.** A recording answers the request it recorded, and no other:

| Part | Matched |
|---|---|
| Method | Ignoring case. |
| Path and query | As `url` matches: `url: /items?page=2`. |
| Headers | Those `RecordRequestHeaders` names, by their first value, or their absence. A redacted one by its presence. No other header. |
| Body | A JSON body as JSON, whatever its spacing and member order. A urlencoded or multipart form by all its text fields. Other text as it is, and no body as none. The media type of the `Content-Type` too. |
| Files of a form | By name, content type and content, as `form_file` patterns. |

Calls a recording can't tell apart are one mock, whose answers play in turn, then the last one again. The same answer every time is one. `RecordRepeats(vlatte.RecordFirst)` keeps the first answer only.

A recording doesn't match what it can't read, and then takes any body: one that isn't UTF-8 text, or is larger than `MaxInlineBody`, or that `MaxBodyParsingLimit` cut, or that a parser of the server's config read, or the urlencoded form of a request other than POST, PUT and PATCH, which Go doesn't read. The forward proxy's host isn't recorded: one path on two hosts is one mock.

**What it answers.** The status, the headers but those the server sets (`Date`, `Content-Length` and the hop-by-hop ones) and `Set-Cookie`, the trailers, and the body as the client got it, after the steps of the mock.
- A text body up to `MaxInlineBody`, 1 MiB by default, is written in the mock file. Other bodies go to files next to it, named after their content, so equal bodies share a file. `RecordBodyFiles(true)` writes every body to a file.
- A gzip body that fits in `MaxInlineBody` decoded is recorded decoded, with `encoding: gzip`, and compressed again when it is served, into bytes that may differ. So a gzip body with a header or trailer that holds for its bytes, a strong `ETag`, `Content-MD5`, `Digest`, `Content-Digest`, `Repr-Digest` or `Content-Range`, is kept as sent, as are other encoded bodies, with their `Content-Encoding`.
- A larger body goes to a file as it streams, so it never sits in memory whole. When the disk is slower than the sender, the answer waits for it, a second in all at most. Past that, or past `MaxBodySize`, 1 GiB by default, the call isn't recorded, and the client still gets every byte.
- A body file plays back streamed, without a `Content-Length`.
- `RecordExcludeResponseHeaders` sets the headers left out, in place of `Set-Cookie`: name it among them to keep leaving it out, or name none to keep every header. `RecordTransform` changes each recording, as Go values, before it is written:

```go
mockhttp.RecordTransform(func(r *mockhttp.RecordedMock) error {
	for i := range r.Responses {
		delete(r.Responses[i].Header, "X-Request-Id")
	}
	return nil
})
```

**Secrets.** Mock files are plain text, and often committed: review them first. They hold the request bodies recordings match, such as a password or a `client_secret`, the values of the headers `RecordRequestHeaders` names, the response headers and bodies, such as tokens, and the query, in the URL and in the file name. `Set-Cookie` is left out by default. The headers `Setup().RedactHeader(...)` names keep their values out too: a response header is written `<redacted>`, and a request header matches by its presence. `RecordTransform` hides the rest.

**Files.** A recording writes a mock file named after its request, such as `GET-items-page-2-<hash>.mock.json`, or `.yaml` or `.yml` with `RecordFormat`, into its directory: `testdata/_mocks_recorded` by default, from `root_dir`. A mock file appears whole, or not at all.

**Status.** `RecordingStatus` counts the calls taken, the mocks written, and the calls dropped or that failed. A response never waits for the calls before it to be written: when too many wait, the next ones are dropped and counted.

**Settings.** `Setup().Record(...)` records from start to close, and so does the `record` section of the config:

```yaml
# .vlatterc.yaml
record:
  target: https://api.example.com
  paths: ["/v1/*"]
  request_headers: [Accept]
  exclude_response_headers: [Set-Cookie, X-Request-Id]
```

The target's TLS certificate is verified: a recording sends the target its requests, credentials included. `record.skip_ssl_verify`, or `RecordSkipSSLVerify(true)`, skips it, such as for a self-signed certificate. In Go, `RecordTransport(rt)` sends through a transport of yours instead, such as one that trusts the target's CA, and `skip_ssl_verify` doesn't apply to it. `vlatte --help` lists every `record` setting.

**Admin API.** The [admin API](#admin-api) starts and stops recordings while the server runs: `POST /recording` takes the keys of the `record` section, `GET /recording` counts the calls, and `DELETE /recording` stops it and lists the files it wrote. Its `/recorder` page does the same from a browser.

## Admin API

The admin API manages a server while it runs. It is an HTTP server of its own, on its own address, the same for mock servers of every protocol, and it leaves every path of the mock server to the mocks. It serves only when asked:

```go
m := mockhttp.MustNew(mockhttp.Setup().Admin("127.0.0.1:8081")) // "" picks a random local port
m.MustStart()
fmt.Println(m.AdminURL()) // http://127.0.0.1:8081
```

From settings, `admin: true` turns it on, and `admin.addr` (`--admin-addr`, `VLATTE_ADMIN_ADDR`) sets its address. A gRPC server serves the same admin API with `mockgrpc.Setup().Admin(addr)`: what gRPC mocks can't do, such as being added from a mock file, answers `501`.

| Route | What it does |
|---|---|
| `GET /health` | Answers once the admin API serves, with the kind of the server and the URL of its mocks. |
| `GET /mocks` | Lists every mock, enabled or not, with its `priority`, `enabled`, `hits` and `origin`. |
| `POST /mocks` | Adds the mock of the body, a [mock file](#mock-files). An id in use is a `409`. |
| `POST /mocks/validate` | Checks a mock as `POST /mocks` would, without adding it. |
| `GET /mocks/{id}` | The mock as a mock file describes it, which `PUT` and `POST` take back. |
| `PUT /mocks/{id}` | Replaces the mock, in one step. The body's id must be the path's. |
| `PATCH /mocks/{id}` | Enables or disables the mock: `{"enabled": false}`. |
| `DELETE /mocks/{id}`, `DELETE /mocks` | Removes the mock, or every mock. |
| `POST /mocks/reload` | Loads the mock files, and the [persistent store](#persistent-admin-mocks), again, as `m.Reload()` does. A mock file's mock takes the place of a mock added through the admin API with its id. |
| `POST /mocks/save` | Writes the mocks added through the admin API as mock files, `<id>.mock.<format>`, and serves them from the files from then on. `{"dir": ..., "format": ...}` defaults to `testdata/_mocks` and YAML, which the default mock file patterns load on the next start; `dir` stays inside `root_dir`. When a mock can't be written as a mock file that loads back, nothing is written, and files that fail to load are put back as they were. |
| `GET /events` | Streams what the server serves as it happens, as [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html): requests a mock answered, requests no mock matched, proxied requests, warnings and errors. Nothing is kept: it is no journal of requests. `curl -N <admin>/events` follows it. |
| `GET /logs` | Streams the server's log lines, as it writes them, as Server-Sent Events. A server given its own `Logger` streams none. |
| `POST /reset` | Removes the mocks added through the admin API, loads the mock files again, saved ones included, and starts every scenario over. Mocks added in code stay as they are. The mocks of a persistent store come back from it: reset never deletes what it holds. |
| `POST /recording`, `GET /recording`, `DELETE /recording` | Starts a [recording](#recording-and-playback) with the keys of the `record` section, counts its calls, and stops it, listing the files it wrote. |
| `GET /recorder` | A page that records a service from a browser. |
| `GET /scenarios`, `PUT /scenarios/{name}`, `DELETE /scenarios` | Lists the state of each scenario, moves one to a state, `{"state": "paid"}`, and starts every scenario over. Names are exact: `Checkout` isn't `checkout`. |
| `GET /openapi`, `GET /docs` | The OpenAPI document, and a page that renders it. The page loads [Scalar](https://scalar.com) from a CDN, so it needs internet. |

**Formats.** Bodies are JSON, or YAML with `Content-Type: application/yaml`. Answers follow `Accept`: JSON, or YAML when it is preferred. A relative `body_file` in a mock sent to the admin API opens from the server's working directory, as `GET /mocks/{id}` writes it.

**Errors** are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem documents with a stable `code`; the problems of a mock are listed one by one:

```json
{
  "status": 400,
  "title": "Bad Request",
  "code": "invalid_body",
  "detail": "the mock has 1 problem",
  "instance": "/mocks",
  "problems": [
    {
      "code": "unknown_key",
      "origin": "admin request",
      "key": "response.stauts",
      "message": "response.stauts is not a known setting",
      "hint": "did you mean response.status?"
    }
  ]
}
```

A request net/http refuses before the admin API reads it, such as one with a malformed request line, is answered in plain text.

**Safety.** The admin API has no authentication: anyone who reaches it can change the mocks, which can read files the server can. It listens on a local address unless told otherwise; with `VLATTE_DOCKER_HOST`, the CLI listens on every interface, as for the mocks, so publish its port only where the network is trusted. A browser's cross-site request that would change the server is refused with `403`.

### Persistent admin mocks

The mocks the admin API adds last as long as the server, unless a persistent store keeps them. Each change the admin API makes, adding, replacing, enabling, disabling or removing a mock, reaches the store at once, and the server serves what the store holds when it starts and on each reload, so a change made to the store directly takes effect then too. The mocks of mock files and code never reach it, and a mock file's mock wins over a mock of the store with its id.

`FileStore(dir)` keeps them as mock files, `<id>.mock.yaml`, which only the store loads: keep `dir` out of the mock file patterns. A relative `dir` starts at `root_dir`. A mock whose description doesn't load back is refused, as its file would fail every later start. From settings, `admin.dir` (`--admin-dir`, `VLATTE_ADMIN_DIR`) sets the directory, and an empty one keeps the mocks in memory.

```go
m := mockhttp.MustNew(mockhttp.Setup().Admin("").AdminStore(mockhttp.FileStore("testdata/_admin")))
```

Any `vlatte.Store[*mockhttp.Mock]` can be the store, such as a database collection. It keeps the descriptions of the mocks by ID: `EncodeMock` writes one, and `DecodeMock` builds the mock back for the server.

```go
mockhttp.Setup().AdminStore(func(app *mockhttp.Server) (vlatte.Store[*mockhttp.Mock], error) {
	return &collectionStore{app: app, coll: coll}, nil // Store: EncodeMock(mock, "json"); PickAll: DecodeMock(app, doc, "json")
})
```

`POST /mocks/save` is apart from the store: it exports the admin API's mocks as mock files to commit, which then serve in place of the store's copies.

## Failures

When no mock matches, or serving a request fails, the server answers itself. It sends `418 I'm a teapot`, a status a real server rarely sends, so the answer never looks like a mock's, and the `VLatte-Error` header says why:

| `VLatte-Error` | Why |
|---|---|
| `no-match` | No mock matched the request. |
| `match` | Finding a mock failed: a matcher, or the store, returned an error. |
| `reply` | A mock matched, and its reply failed: a template, a body file, a template data provider or a response mapper. |
| `panic` | Serving the request panicked. |

The body says the same in text, and names the mock and the error. A test checks the header:

```go
require.Equal(t, vlatte.FailureReply, res.Header.Get(vlatte.FailureHeader))
```

The status of `no-match` is `no_match_status_code`, and the status of the others is `error_status_code`. Both default to 418.

A gRPC server answers `NotFound` when no mock matches, and `Internal` when serving a call fails, with the same values in the `vlatte-error` trailer. A mock that answers with a status, as `Unary().Status(...)` does, gets no trailer: that status is its answer.

## Debug headers

Each answer says how the server served it, in `VLatte-*` headers:

| Header | Value |
|---|---|
| `VLatte-Matched-Mock-Id` | The ID of the mock that matched. |
| `VLatte-Matched-Mock-Name` | Its name, when it has one. |
| `VLatte-Matched-Mock-Hits` | Its hits, this request included. |
| `VLatte-Matched-Mock-Source` | The file it was loaded from, when it was loaded from one. |
| `VLatte-Request-Id` | The ID of the request: `.Request.ID` in templates, `request_id` in the logs. Every answer has it, failures and no-match included. |
| `VLatte-Proxied` | `true` when a real service answered instead of a mock: a mock that replies with `From(...)`, a proxy passing on a request no mock matches, or the target of a recording. |

A failure keeps the headers of the mock that matched before it failed. Hits count once the answer is sent, so two requests served at the same time can show the same number. A test checks them like any header:

```go
require.Equal(t, "users-get", res.Header.Get(vlatte.MatchedMockIDHeader))
```

Header names don't depend on case: Go sends `Vlatte-Matched-Mock-Id`, and HTTP/2 sends them in lower case. Recordings leave the debug headers out.

`debug_headers: false` turns them all off, as do `VLATTE_DEBUG_HEADERS=false`, `--debug-headers=false` and `mockhttp.Setup().NoDebugHeaders()`. `VLatte-Error` stays: it is not a debug header.

A gRPC server sends `vlatte-matched-mock-id`, `vlatte-matched-mock-name` and `vlatte-matched-mock-hits` as header metadata, with a mock's message or status. `Name(...)` on a mock builder sets the name. `mockgrpc.Setup().NoDebugHeaders()` turns them off.

## Configuration

A server's settings come from these sources, each overriding the ones before it:

1. Defaults.
2. Code: the options passed to `mockhttp.New`, such as `mockhttp.Setup().Addr(":8080")`.
3. A `.vlatterc.json`, `.vlatterc.yaml` or `.vlatterc.yml` file in the working directory or in `testdata`. `--config` or `VLATTE_CONFIG` names another file.
4. `VLATTE_*` environment variables.
5. Command-line flags, for the `vlatte` command.

Each setting has a key that its variable and flag derive from: `log.level` is `log: {level: debug}` in a file, `VLATTE_LOG_LEVEL=debug` and `--log-level=debug`. `vlatte --help` lists every setting.

In a file, each setting has its type, as in mock files: `addr: ":8080"` in quotes, `max_age: 150` bare, and a list as a list. Environment variables and flags are text:
- bools read as `strconv.ParseBool` reads them;
- numbers are written in the JSON number grammar;
- lists are comma-separated.

`log.level` and `log.verbosity` take a name, in any case, such as `info`.

```yaml
# .vlatterc.yaml
addr: ":8080"
log:
  level: debug
proxy:
  timeout: 1.5s # or milliseconds: 1500
cors: true
```

Setting a key of a section (`cors`, `proxy`, `proxy_all` or `record`) turns the section on, and `false` turns it off.

`root_dir` is the directory that relative mock file patterns and record directories start at. Absolute ones are used as they are.

Unknown keys, flags and `VLATTE_*` variables are errors, except `VLATTE_DOCKER_HOST`. A server that can't load its settings reports every problem at once:

```
server: error loading configuration sources
config: 2 problems
  env VLATTE_LOGLEVEL: unknown_key: VLATTE_LOGLEVEL is not a known setting; did you mean VLATTE_LOG_LEVEL?
  file .vlatterc.yaml: invalid_value: proxy.timeout must be milliseconds or a Go duration, not "10x"; such as 1500 or 1.5s
```

Servers in tests read the file and the environment too. Pick the sources with `ConfigSources`, or pass none to use code only:

```go
m := mockhttp.MustNew(mockhttp.Setup().ConfigSources())
m = mockhttp.MustNew(mockhttp.Setup().ConfigSources(mockhttp.ConfigFile("testdata/server.yaml")))
```

## CLI

`vlatte` serves the mock files under `testdata/_mocks` in the working directory:

```sh
vlatte --addr=127.0.0.1:8080
curl http://127.0.0.1:8080/hello
```

Without `--addr`, it listens on a random port and prints the URL. Settings can also come from a `.vlatterc` file or `VLATTE_*` environment variables; see [Configuration](#configuration). Run `vlatte --help` to list them, and `vlatte completion --help` to set up shell completion.

Mock files can send [webhooks](#webhooks): the CLI registers the `webhook` listener, and `--webhook-timeout` bounds each request.

It [records](#recording-and-playback) a service until it stops, and serves the recordings on the next run:

```sh
vlatte --record --record-target=https://api.example.com --record-paths='/v1/*'
# Ctrl-C writes the recordings into testdata/_mocks_recorded; another Ctrl-C quits at once
vlatte
```

It serves the [admin API](#admin-api) too, on an address of its own, which it prints: a random local port unless `--admin-addr` sets one, and `--admin=false` turns it off. With `VLATTE_DOCKER_HOST` set, it listens on port 8081 of that host, as the mocks do on 8080. `POST /mocks` adds a mock sent in the request body, for example as JSON:

```sh
vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","path":"/added"},"response":{"status":201}}' \
  http://127.0.0.1:8081/mocks
```

The command keeps the mocks its admin API adds in `testdata/_admin`, as [persistent admin mocks](#persistent-admin-mocks), so the next run serves them: `--admin-dir` moves them, and `--admin-dir=""` keeps them in memory.

## Development

```sh
make test   # run the tests with the race detector
make lint   # run golangci-lint
```

## License

[MIT](LICENSE)
