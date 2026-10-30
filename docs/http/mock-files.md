# Mock files

Mocks can also live in YAML or JSON files (`.json`, `.yaml` or `.yml`).

- **On start,** the server loads the files matching `testdata/vlatte/_mocks/*mock.yaml` and `testdata/vlatte/_mocks/*mock.yml`, and the [recordings](/recording/) in `testdata/vlatte/_mocks_recorded`, in name order, then YAML document order: the folders of its [work directory](/run/configuration#work-directory). Change the patterns with `Setup().MockFilePatterns(...)`.
- **To load a file with multiple mocks,** set `Setup().MockFilePatterns(path)`. To build one mock explicitly, use `m.MustMock(mockhttp.FromFile(path))`.
- **`m.Reload()`** replaces the mocks of the files with what they hold now, in one step. When a file has problems, it returns them and the mocks stay as they were.

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
    method: GET, path: /first
response: 
    status: 201, body: first
---
id: second
request: 
    method: GET, path: /second
response: 
    status: 202, body: second
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
