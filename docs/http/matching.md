# Request matching

A mock matches a request when every matcher it has matches. Each part of the request has a builder method in Go and a key under `request` in a [mock file](/http/mock-files). The matchers themselves, such as `match.Equal` or `[contains, x]`, are shared by every protocol: see [Matchers](/matchers/).

## What a mock can match

| Go | Mock file | Matches |
|---|---|---|
| `Method(methods...)`, `MethodMatches(m)` | `method` | The method, ignoring case: one of several, such as `method: get, post`. |
| `Scheme(s)`, `SchemeMatches(m)` | `scheme` | `http` or `https`. |
| `Host(m)` | `host` | The host the request names, without its port. Text ignores case. |
| `URL(m)`, `URLf(format, a...)` | `url`, `url_match` | The URL. See [URLs](#urls). |
| `RequestURI(uri)` | `url` (relative) | The path and query, exactly. |
| `URLPath(m)`, `URLPathf(format, a...)` | `path` | The path. `URLPathf` and `Getf` take a glob. |
| `URLPathTemplate(t)`, `PathParam(name, m)` | `path_template`, `path_params` | A path of a shape, and its parameters. See [Path templates](#path-templates). |
| `URLFragment(m)` | `fragment` | The decoded text after a `#` in the request target. |
| `Query(name, m)`, `Queryf`, `Queries(name, m)` | `query`, `queries` | Query parameters. See [Many values](#many-values). |
| `Header(name, m)`, `Headerf`, `Headers(name, m)` | `header`, `headers` | Headers, by name in any case. |
| `ContentType(value)` | `header: {content-type: ...}` | The `Content-Type`, ignoring case. A value without parameters matches the media type alone. |
| `Cookie(name, m)` | `cookie` | Cookies by name. |
| `BasicAuth(user, pass)`, `BasicAuthMatches(user, pass)` | `basic_auth` | Basic credentials. |
| `ClientIP(m)` | `client_ip` | The client's address, as the server's connection has it, not `X-Forwarded-For`. |
| `FormField`, `FormFields`, `FormFile` | `form`, `forms`, `form_file` | Form fields and files: see [Forms and multipart](/http/forms-multipart). |
| `Body(matchers...)` | `body` | The body. See [JSON and XML bodies](#json-and-xml-bodies). |
| `Matches(m)`, `RequestMatches(fn)` | `custom` | The whole `*http.Request`. See [Custom matchers](/matchers/custom). |

```go
m.MustMock(mockhttp.Postf("/pets").
	Header("Authorization", match.HasPrefix("Bearer ")).
	Query("dry_run", match.Equal(true)).
	Body(match.JSONPath("$.name", match.Equal("Rex"))).
	Reply(mockhttp.Created()))
```

```yaml
request:
  method: POST
  path: /pets
  header:
    Authorization: [hasPrefix, "Bearer "]
  query:
    dry_run: [equal, true]
  body: [jsonpath, $.name, [equal, Rex]]
```

## Text and types

Most of a request is text: its path, query, headers and form values. A matcher reads that text as the type it compares with:

- `[equal, 10]` and `[isIn, [10, 20]]` match `?page=10`;
- `[gt, 18]` matches `?age=30`;
- `[equal, true]` matches `?debug=1`.

No other reading happens: `[equal, 10]` doesn't match `010`.

**Keys** are kept as written, so a query name like `userId` matches `userId` only. Header names, and methods written as text, ignore case, so `method: get, post` matches `POST`; cookie names don't.

## URLs

`url: /items?page=2` matches that path and query exactly: the same parameters, in any order, each with its values in order, so `/items` and `/items?page=2&sort=asc` don't match. Path segments compare decoded, so `%41` is `A`, but `%2F` isn't `/`.

A relative `url` with a `*` in its path is an error: match a path with a glob through `path: /items/*`, and parameters one by one through `query`. For a literal `*`, write `%2A`.

An absolute `url`, such as `https://api.example.com/items`, compares as text ignoring case, with the server's URL, or the one a request names in absolute form, as the [forward proxy](/http/proxying#forward-proxy) gets it, or intercepted, without its default port. In Go, `RequestURI("/items?page=2")` matches as a relative `url` does.

## Path templates

`path_template: /users/{id}/orders/{orderId}` matches paths of its shape: as many segments, the literal ones the same once decoded, and a non-empty one for each parameter, so `/users/7/orders` and `/users/7/orders/9/` don't match. A parameter is a whole segment; write a literal brace as `%7B` or `%7D`.

`path_params` matches the parameters by name, decoded, and [templates](/templating/) read them as `.Request.PathParams.id`, or `index .Request.PathParams "order-id"` for a name that isn't a Go identifier. `url`, `url_match` or `path` can match the request too. In Go, `URLPathTemplate` and `PathParam`.

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

## Many values

`query`, `header` and `form` read the first value of a name. `queries`, `headers` and `forms` (`Queries`, `Headers` and `FormFields` in Go) read every value, as a list, so they take matcher lists, such as `[contains, a]`, `[itemsMatch, [a, b]]` or `[len, 2]`: they refuse text, which never equals a list.

A name the request doesn't have is an empty list, which `[not, present]` matches, and a header sent empty isn't. A header line such as `a, b` is one value.

## More of the request

- `cookie` matches cookies by name, as `Cookie` does; a cookie the request doesn't have is null, which no text matches.
- `basic_auth: {username: jeff, password: s3cret}` matches Basic credentials, as `BasicAuth` does, and `BasicAuthMatches` takes matchers; without credentials, the username and password are null, which no text matches.
- `host` matches the host the request names, without its port, and text ignores case: `Host` in Go.
- `client_ip` matches the client's address, as the server's connection has it, not `X-Forwarded-For`: `ClientIP` in Go.

## JSON and XML bodies

`[eqj, {...}]` matches a JSON body equal to a document, `[jsonschema, schema]` one that conforms to a JSON Schema, `[eqx, '...']` an equal XML document, and `[jsonpath, ...]` and `[xpath, ...]` match what a path selects. Their options, such as `ignore_array_order`, are in [JSON and XML](/matchers/json-xml).

```yaml
request:
  method: POST
  path: /pets
  body: [all, [
    [eqj, {name: Rex, tags: [a, b]}, {ignore_array_order: true, ignore_extra_elements: true}],
    [jsonpath, $.id, [regex, '^[0-9]+$']]
  ]]
```

## Binary bodies

`[equalbytes, "//4A"]` matches a body whose bytes are those of the standard base64 given, whose line breaks are ignored, exactly: no text reads as a number. In Go, `EqualBytes(b)`.

`Equal(b)` matches the same bytes, but a mock's description, which the admin API serves, writes them as base64 text, which loads back as `[eq, "<base64>"]`, another matcher: `EqualBytes` describes them as `[equalbytes, ...]`, so the mock loads back.

## Request bodies

Go's `net/http` parses request bodies by their media type, in any case: `text/plain` and `application/json` as text, and the two form types as forms. `Setup().MaxBodyParsingLimit(n)` bounds how much of a body the server reads to match it, `Setup().NoBodyParsing()` reads none, and `Setup().RequestBodyParsers(...)` adds parsers of your own.
