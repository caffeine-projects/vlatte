# Mock files

Mocks can also live in YAML or JSON files (`.json`, `.yaml` or `.yml`).

- **On start,** the server loads the files matching `testdata/vlatte/_mocks/*mock.yaml` and `testdata/vlatte/_mocks/*mock.yml`, and the [recordings](/recording/) in `testdata/vlatte/_mocks_recorded`, in name order, then YAML document order: the folders of its [work directory](/run/configuration#work-directory). Change the patterns with `Setup().MockFilePatterns(...)`.
- **To load a file with multiple mocks,** set `Setup().MockFilePatterns(path)`. To build one mock explicitly, use `m.MustMock(mockhttp.FromFile(path))`.
- **`m.Reload()`** replaces the mocks of the files with what they hold now, in one step. When a file has problems, it returns them and the mocks stay as they were.

For example, this test serves the discovered fixtures and adds a health endpoint in Go.

```go
m := mockhttp.MustNew()
t.Cleanup(m.Close)
m.MustStart()
m.MustMock(mockhttp.Getf("/health").Reply(mockhttp.OK()))
```

```yaml
# testdata/vlatte/_mocks/hello_mock.yaml
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

[`mockhttp/schema/http.schema.json`](/reference/schemas) describes the format, so editors can validate mock files. Its address is `https://vlatte.dev/schemas/http.schema.json`: start a YAML mock file with

```yaml
# yaml-language-server: $schema=https://vlatte.dev/schemas/http.schema.json
```

and editors that use the YAML language server, such as VS Code with the YAML extension, complete and check it. A JSON mock file names it in a `"$schema"` key. See [Mock file schemas](/reference/schemas) for other editors.

## Multiple mocks in a YAML file

Separate mock definitions with `---`:

```yaml
id: first
request:
  method: GET
  path: /first
response:
  status: 201
  body: first
---
id: second
request:
  method: GET
  path: /second
response:
  status: 202
  body: second
```

The file loader reads and validates the complete file before installing its mocks. Mocks of equal priority match in file-name order, then document order. Each relative `body_file` starts at the YAML file's directory. A bad document or duplicate mock ID fails the load; a failed reload keeps the previous complete set. Syntax and schema errors in a file with multiple documents identify the file and document number.

Empty documents are skipped. A file with only empty documents contributes no mocks, so emptying a file and reloading removes its previous mocks. Explicit `null`, scalar and list documents are invalid mock definitions. Anchors belong to their own document.

`FromFile`, `FromBytes` and `DecodeMock` build one mock and reject multiple nonempty YAML documents. JSON files still hold one mock object. This feature does not merge server settings across YAML documents.

## Keys

| Key | What it holds |
|---|---|
| `id`, `name`, `priority`, `enabled` | The mock's [features](/http/mocks#mock-features). |
| `request` | The [matchers](/http/matching). |
| `response` | The [reply](/http/replies). |
| `response_sequence`, `response_random` | [Sequences](/http/replies#sequences) and [random replies](/http/replies#random-replies). |
| `proxy`, `proxy_all` | [Proxy replies](/http/proxying#proxy-replies) and [proxy all](/http/proxying#proxy-all). |
| `mcp`, `mcp_result` | An [MCP server mock](#mcp-server-mocks): its endpoint mock, or a tool mock and what it answers. |
| `delay`, `repeat` | A delay before replying, and how many requests the mock serves: see [Mock lifecycle](/more/mock-lifecycle). |
| `scenario` | A [scenario](/scenarios/). |
| `on_complete` | [Events such as webhooks](/webhooks/). |
| `pipes`, `wire_pipes`, `upstream_pipes` | [Faults](/http/faults#in-mock-files). |
| `ext` | Settings for the server's mock file handlers, `Setup().MockFileHandlers(...)` in Go. |

## Types

Each key takes one type, as the schema says. Write text in quotes where a string goes, as in `name: "123"`, and numbers bare, as in `priority: 10`. Bools read the way Go's `strconv.ParseBool` reads them, so `true`, `"true"`, `t` and `1` are all true. A value of the wrong type is an error that says how to write it.

## Bodies

A reply's `body` is text as is, and any other value as JSON. A body that isn't UTF-8 text, such as an image or text in another encoding, goes in `body_base64`, as standard base64 (`body_base64: //4A`), whose line breaks are ignored, or in a `body_file`. A reply takes one of `body`, `body_base64`, `body_file`, `multipart` and `event_stream` at most.

## Durations

`delay`, `proxy.timeout`, the durations of [faults](/http/faults) and the `delay` and `timeout` of a [webhook](/webhooks/) take whole milliseconds (`1500`) or a Go duration (`1.5s`). `"1500"` in quotes is neither.

## Matching

The `request` key matches requests as the Go builder does: see [Request matching](/http/matching) for every key, and [Matchers](/matchers/) for the matcher lists, such as `[contains, x]` or `[jsonpath, $.id, [gt, 1]]`, and [Custom matchers](/matchers/custom) for matchers of your own, in `request.custom`.

## MCP server mocks

A mock with `mcp` plays an [MCP](https://modelcontextprotocol.io) server over Streamable HTTP: `MCP(path)` in Go. It matches requests to exactly `mcp.endpoint`, so `/mcp` is neither `/mcp/` nor `/mcp/v2`, and REST mocks and other MCP servers, each at its own path, share the server. An MCP server takes two kinds of mocks:

- **The endpoint mock**, with `mcp.server` (`MCPServerInfo` in Go). It answers every request to its path but a tool call: `initialize`, with the client's protocol version when it knows it; `ping`; notifications, with `202`; `tools/list`, with the tools of the enabled tool mocks of its path, one per name, as the first of them in match order describes it; another method with JSON-RPC error `-32601`; a body that isn't JSON with `-32700`, which names `MaxBodyParsingLimit` when the server read only part of the body; and another HTTP method than `POST` with `405`. It takes no response key.
- **A tool mock**, with `mcp.tool` (`MCPTool` in Go). It answers the calls of one tool. `arguments` match the arguments of the call, by name, as `request.header` matches headers. A number compares by its exact value, so `[equal, 9007199254740993]` matches that integer and not its neighbor. Its reply is `mcp_result`, or any other response key, such as `proxy`.

```yaml
id: weather
mcp:
  endpoint: /mcp
  server: {name: weather, version: 1.0.0}
---
id: forecast-lisbon
request:
  header:
    authorization: [hasprefix, "Bearer "]
mcp:
  endpoint: /mcp
  tool:
    name: forecast
    description: The forecast of a city
    input_schema: {type: object, properties: {city: {type: string}}, required: [city]}
    arguments: {city: [equal, Lisbon]}
mcp_result:
  json: {city: Lisbon, sky: clear}
```

`mcp_result` takes one of `text`, `json`, which answers the value as JSON text, and `error`, a JSON-RPC error with `code` and `message`. `is_error: true` makes a `text` the tool's own error. A `text` can be a template, with `template`: the arguments of the call are `{{ (fromJSON .Request.Body.String).params.arguments.city }}`.

A tool call that no tool mock matches gets JSON-RPC error `-32602` on the path of an endpoint mock, with status `200` and `VLatte-Error: no-match`, and the journal records it as a request no mock matched. Its `data.near_misses` lists the mocks that came closest and the parts that didn't match, such as `mcp arguments.city`. The setting `no_match_mcp_status_code` (`Setup().MCPNotFoundStatusCode(code)` in Go) sets another status; an MCP client may take any status but `2xx` as a failure of the transport, and drop the error. A `proxy_all` forwards the call instead.

An MCP mock sets its own method and path: `request.method`, `url`, `url_match`, `path`, `path_template` and `path_params` don't go with `mcp`, and neither does `proxy_all`. Other `request` keys, such as `header`, match as on any mock, and so do `scenario`, `repeat` and `delay`. Two endpoint mocks at one path in mock files fail to load. A server with `NoBodyParsing` builds no MCP mock: MCP mocks read the body.

## Proxy replies

A `proxy` reply (`From` in Go) answers with what its target answers: see [Proxying](/http/proxying).

## Templates

A mock file is plain JSON or YAML. Only the settings that are templates, such as a templated body, render: see [Templates](/templating/).

## Errors

A file with problems isn't loaded. The error lists every problem with its key and a fix:

```
mock: 2 problems
  file testdata/vlatte/_mocks/users_mock.yaml: unknown_key: response.stauts is not a known setting; did you mean response.status?
  file testdata/vlatte/_mocks/users_mock.yaml: conflict: proxy is set with response; keep one of them
```
