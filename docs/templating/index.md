# Templating

Replies can render parts of the response from the request: header values, the body, and the name or content of a body file. So can multipart parts and webhooks.

```yaml
request:
  method: POST
  path: /users

response:
  status: 201
  header_template:
    location: '/users/{{ .Request.Header.Get "X-User-Id" }}'
  body: '{"id": {{ uuid | toJSON }}, "name": {{ (fromJSON .Request.Body.String).name | toJSON }}}'
  template:
    enabled: true
```

In Go:

```go
m.MustMock(mockhttp.Postf("/users").Reply(mockhttp.Created().
	HeaderTemplate("Location", `/users/{{ .Request.Header.Get "X-User-Id" }}`).
	BodyTemplate(`{"id": {{ uuid | toJSON }}}`)))
```

## What is a template

Nothing else renders: `{{` anywhere else is text.

- `header_template` values (`HeaderTemplate`).
- With `template.enabled`: a text `body` (`BodyTemplate`), the content of a `body_file` (`BodyFileWithTemplate`), the text of `multipart` parts (`Template()`), and the text of `event_stream` [events](/sse/events#templated-events) (`Template()`).
- `body_file` names, always. The content of the file is written as it is unless `template.enabled` is set.
- Webhook parameters: `url_template`, `header_template`, `body_template` and `multipart_template`.

## Data

Templates read:

| Field | What it holds |
|---|---|
| `.Request.Method`, `.Request.URL`, `.Request.URLPathSegments`, `.Request.Header`, `.Request.ID` | The request. `index .Request.URLPathSegments 0` is the first segment of the path. |
| `.Request.PathParams.<name>` | The parameters of the mock's [path template](/http/mock-files), decoded. |
| `.Request.Body.String`, `.Request.Body.Content`, `.Request.HasBody` | The body. JSON bodies are text: read their fields with `fromJSON`. |
| `.Request.Cookie "name"`, `.Request.PostForm.Get "f"`, `.Request.FormFile "f"` | Cookies and forms. |
| `.Server.URL` | The server's base URL. |
| `.Parameters.Get "key"`, `.Parameters.Has "key"` | The server's parameters. |
| `.Data` | The map set with `m.SetData`. |
| `.Provided.<key>` | What `Setup().TemplateDataProviders(providers)` provide. |
| `.Extra` | The reply's `template.data`, or `SetTemplateData` in Go. Webhooks don't have it. |
| `.Scenarios.State "name"` | The state of a [scenario](/scenarios/). |
| `.Scenarios.StateOf "name" "key"` | The state of the instance of a [scenario](/scenarios/) of a key. |

## Functions

Besides those of Go templates, such as `if`, `range`, `eq`, `len`, `index`, `slice`, `printf`, `html` and `urlquery`, templates call the functions below. The value a function works on comes last, so it pipes: `{{ .Request.Header.Get "X-Name" | trimSpace | toUpper }}`. List functions take the list first, as Go's `append` does. A bad argument, such as an unknown algorithm or a division by zero, fails the template.

| Kind | Functions |
|---|---|
| JSON | `toJSON v`, `fromJSON text`, `JSONPath path v` (the first value a JSONPath selects in a value or JSON text, or nil), `JSONPathAll path v`, `deleteJSONPath path v`, `mergePatch patch v` ([RFC 7386](https://www.rfc-editor.org/rfc/rfc7386)); each `v` and `patch` is a value or JSON text, so a string is JSON text; `indentJSON text`, `compactJSON text` |
| Strings | `toUpper`, `toLower`, `trimSpace`, `title`, `upperFirst`, `truncate max s`, `replaceAll old new s`, `split sep s`, `join sep list`, `substring start end s` (end -1 is the end), `slug`; they count runes, not bytes |
| Regexps | `regexpFind pattern s`, `regexpSubmatch pattern s`, `regexpReplaceAll pattern repl s` (`$1` is a group), `regexpMatch pattern s`, in Go's syntax |
| Values | `contains item collection` (a substring, a list item or a map key), `default fallback v` (when v is nil, `""` or empty; 0 and false are values) |
| Numbers | `add`, `sub`, `mul`, `div`, `mod` (ints when both operands are integers, as in Go, so `div 7 2` is 3; float64 otherwise), `toInt v` and `toFloat v`, which convert a numeric string, such as a query parameter: arithmetic takes numbers only |
| Lists | `list items...`, `append list item`, `insert list index item`, `remove list index` (-1 is the last), `randomItem list`, `shuffle list`, `reverse list`, `sortBy list path` (by what a JSONPath selects in each item, `"$"` for plain values; stable, missing values first). They return new lists. |
| XML | `xpath expr xml` (the text of what an XPath 1.0 expression selects, or the number or boolean it computes), `xpathNodes expr xml` (each node's `.Name`, `.Text` and `.Attrs`; printing an element writes its XML) |
| Time | `now`, `formatTime layout t` and `parseTime layout s` (a Go layout, `RFC3339` and the other names of package `time`, `unix` or `unixMilli`), `timeAdd offset t` (`"3 days"`, `"-1 month"` or a Go duration such as `"1h30m"`), `inZone zone t`, `truncateTime point t` (`"first day of month"`, `"last day of year"`, ...) |
| Encoding | `base64Encode`, `base64Decode`, `queryUnescape`, `parseQuery text` (form text as `url.Values`: `(parseQuery $s).Get "a"`) |
| Random | `uuid`, `randomString kind length` (`alphanumeric`, `alphabetic`, `numeric`, `alphanumericSymbols` or `hex`), `randomInt min max` and `randomFloat min max` (in [min, max)). Not for secrets. |
| Hashes | `hash alg encoding s` (`md5`, `sha1`, `sha224`, `sha256`, `sha384`, `sha512`, `sha3-224` to `sha3-512`; `hex` or `base64`), `hmac alg key encoding s` (`sha1`, `sha256` or `sha512`), as webhook providers sign payloads: `{{ hmac "sha256" "secret" "hex" .Request.Body.String }}` |
| Environment | `env name`: see below |

```yaml
response:
  body: |
    {"id": {{ JSONPath "$.order.id" .Request.Body.String | toJSON }},
     "expires": {{ now | timeAdd "30 days" | formatTime "RFC3339" | toJSON }},
     "items": {{ JSONPathAll "$.order.items[*].sku" .Request.Body.String | len }}}
  template:
    enabled: true
```

## Environment variables

`env` reads only the variables the server's settings name, by exact name: `template.env` (`--template-env API_URL,TENANT`, `VLATTE_TEMPLATE_ENV`, or `Setup().TemplateEnv(...)` in Go) and `grpc.template.env` for gRPC mocks. Without it, templates read none, so a mock file can't print the server's secrets. A mock whose template names a variable that isn't allowed fails as it loads; a name read from the request is checked when it renders. An allowed variable that isn't set is `""`: `{{ env "TENANT" | default "acme" }}`. On an engine of your own, allow them with `vlatte.WithEnv`.

Add your own functions to the engine:

```go
engine := vlatte.MustNewGoTemplateEngine(vlatte.WithTemplateFuncs(map[string]any{"upper": strings.ToUpper}))
m := mockhttp.MustNew(mockhttp.Setup().TemplateEngine(engine))
```

## Missing keys

Missing keys are errors. `{{ .Extra.nmae }}` fails the request and names the key, instead of writing `<no value>`, and so does `.Parameters.Get` of a parameter that isn't set. Read an optional key with `index`, as in `{{ with index .Extra "nick" }}{{ . }}{{ end }}`, and an optional parameter with `.Parameters.Has` first. `vlatte.WithMissingKey` changes the rule for map keys.

## Escaping

Templates are Go's `text/template`, so a body can be any format and nothing is escaped for you. Write JSON values with `toJSON`, and use `html` or `urlquery` where they fit.

## Errors

Templates compile when the mock is built, so one that doesn't compile fails the mock. A body file whose content is a template compiles when a request reads it, since its name can depend on the request. A template that fails at a request answers as a [failure](/http/troubleshooting#failures): the error status, `VLatte-Error: reply`, and a body that names the mock and the error.

## File names

A body file name rendered from the request stays inside the directory written before the template: `files/{{ .Request.URL.Query.Get "f" }}` opens files in `files` only, and refuses `../` or an absolute path from the request.

## Header values

Header values are written without the spaces and line breaks around them, as `net/http` writes headers, so a YAML block scalar works. A line break inside a rendered value fails the reply, or the webhook, so the request can't add a header.

## Webhooks

Webhooks compile their templates when the mock is built, and render them each time they run, with the data of the request that completed the mock.

## Other engines

A server compiles all its templates with one `vlatte.TemplateEngine`. vlatte ships `vlatte.GoTemplateEngine`; another engine, such as one for Handlebars, plugs in by implementing two methods:

```go
type TemplateEngine interface {
	Compile(name, text string) (Template, error)
}

type Template interface {
	Execute(w io.Writer, data any) error // safe for concurrent use
}
```

Pass it with `Setup().TemplateEngine(e)`. Templates then use its syntax everywhere, mock files included, as in `{{Request.Method}}` for Handlebars. [`template_handlebars_test.go`](https://github.com/caffeine-projects/vlatte/blob/main/mockhttp/e2e/template_handlebars_test.go) wraps [raymond](https://github.com/mailgun/raymond) in a few lines. Other engines read the same fields, maps and methods without arguments; methods with arguments, such as `.Parameters.Get "key"`, are Go template syntax.

## In gRPC

A gRPC mock renders its messages with `Template(texts...)`, `template` in a [gRPC mock file](/grpc/mock-files): each text is a template of a message, as JSON, rendered for each call, then read as the method's output type. It reads the same engine, functions and settings, with the call in place of the HTTP request:

| Field | What it holds |
|---|---|
| `.Request.Message.<field>` | The request, by the field names of the `.proto` file. |
| `.Request.Header` | The call's metadata. |
| `.Parameters`, `.Data`, `.Provided.<key>` | As for HTTP: `m.SetData` sets `.Data`, and `Setup().TemplateDataProviders(providers)` provide `.Provided`. |
| `.Scenarios.State "name"`, `.Scenarios.StateOf "name" "key"` | The state of a [scenario](/scenarios/). |

```go
m.MustMock(mockgrpc.Method("greeter.Greeter/SayHello").
	Reply(mockgrpc.Template(`{"message": "hi {{ .Request.Message.name }}"}`)))
```

`grpc.template.env` names the environment variables gRPC templates may read.

