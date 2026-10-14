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

A recipe prepares a server through `Prep`. This one forwards every request that no mock matches:

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(proxyall.Config{Target: "https://api.example.com"}))
```

## Mock files

Mocks can also live in YAML or JSON files (`.json`, `.yaml` or `.yml`).
- **On start,** the server loads the files matching `testdata/_mocks/*mock.yaml` and `testdata/_mocks/*mock.yml`, in name order. Change the patterns with `Setup().MockFilePatterns(...)`.
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

**Keys.** Keys are kept as written, so a query name like `userId` matches `userId` only.

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

**Errors.** Templates compile when the mock is built, so one that doesn't compile fails the mock. A body file whose content is a template compiles when a request reads it, since its name can depend on the request. A template that fails at a request answers as a [failure](#failures): the error status, `X-Vlatte-Error: reply`, and a body that names the mock and the error.

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

**Fields.** `FormField` (file key `form`) reads a field of either encoding, as `http.Request.PostFormValue` does: its first value, and never the query.

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

**Recording** writes a recorded upload as `form` and `form_file` patterns, so it replays whatever boundary the next client picks. Files that aren't UTF-8 text go to files next to the recording. A replay matches the recorded fields and files, as `FormField` and `FormFile` do: more fields or files still match.

## Webhooks

A webhook is a request a mock sends after it replies, as a payment API calls back to say an order was paid. The `webhook` package sends them, as an `on_complete` listener of the server.

**Registering.** A mock names its listener, so the server registers one under that name first. The `vlatte` command registers a webhook as `webhook`. In Go:

```go
wh, err := webhook.New()
m := mockhttp.MustNew(mockhttp.Setup().OnCompleteListener("webhook", wh))
```

A mock that names a listener the server doesn't have fails to build, and so does a server given two listeners of one name.

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

**Failures.** A webhook fails when its target doesn't answer in time, or answers with a status outside 2xx: a redirect isn't followed. The server logs the failure as a warning. The timeout is 10 seconds: `webhook.WithTimeout` changes it in Go, and the `webhook.timeout` setting for the `vlatte` command, where 0 means 10 seconds, unlike `proxy.timeout`. A response waits for a webhook at most its delay plus the timeout.

**Logs.** Webhooks log each request and its answer with the server's logger, so `Setup().Silent()` silences them too. `webhook.WithLogger` gives them a logger of their own.

## Failures

When no mock matches, or serving a request fails, the server answers itself. It sends `418 I'm a teapot`, a status a real server rarely sends, so the answer never looks like a mock's, and the `X-Vlatte-Error` header says why:

| `X-Vlatte-Error` | Why |
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

A gRPC server answers `NotFound` when no mock matches, and `Internal` when serving a call fails, with the same values in the `x-vlatte-error` trailer. A mock that answers with a status, as `Unary().Status(...)` does, gets no trailer: that status is its answer.

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

Setting a key of a section (`cors`, `proxy` or `record`) turns the section on, and `false` turns it off.

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

While it runs, `POST /__admin/mocks` adds a mock sent in the request body, for example as JSON:

```sh
curl -X POST -H 'Content-Type: application/json' \
  -d '{"request":{"method":"GET","path":"/added"},"response":{"status":201}}' \
  http://127.0.0.1:8080/__admin/mocks
```

## Development

```sh
make test   # run the tests with the race detector
make lint   # run golangci-lint
```

## License

[MIT](LICENSE)
