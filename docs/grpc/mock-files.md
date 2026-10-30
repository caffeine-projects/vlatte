# Mock files

`Setup().MockFiles("testdata/grpc/*.yaml")` loads gRPC mock files at `Start`, and `m.Reload()` again; `mockgrpc.FromFile` and `FromBytes` build one. Their keys mirror the Go API, and [`mockgrpc/schema/grpc.schema.json`](/reference/schemas) describes them for editors:

```yaml
id: denied
priority: 1
request:
  method: users.Users/Get
  header:
    authorization: [hasprefix, "Bearer "]
  fields:
    id: [regex, "^u-"]
response:
  status:
    code: PERMISSION_DENIED
    message: not yours
    details:
      - "@type": type.googleapis.com/google.rpc.ErrorInfo
        reason: NOT_OWNER
```

Start a YAML file with `# yaml-language-server: $schema=https://vlatte.dev/schemas/grpc.schema.json` for completion and checks in editors, or give a JSON file a `"$schema"` key: see [Mock file schemas](/reference/schemas).

## Multiple mocks in a YAML file

The `MockFiles` loader reads every mock separated by `---` in each YAML file:

```yaml
id: first
request:
  method: users.Users/Get
  fields: {id: u-1}
response:
  status: {code: NOT_FOUND}
---
id: second
request:
  method: users.Users/Get
  fields: {id: u-2}
response:
  status: {code: PERMISSION_DENIED}
```

Mocks of equal priority match in file-name order, then document order. Relative `body_file` paths start at the YAML file's directory. Empty documents are skipped; a file with only empty documents contributes no mocks. Explicit `null`, scalar and list documents are invalid mock definitions. Anchors belong to their own document.

The complete load is validated before any mocks are installed. A bad document or duplicate mock ID fails the load; a failed reload keeps the previous complete set. Syntax and schema errors in a file with multiple documents identify the file and document number. Removing a document, or emptying a file, removes its old mocks after a successful reload.

`FromFile`, `FromBytes` and `DecodeMock` build one mock and reject multiple nonempty YAML documents. JSON files still hold one mock object. Server settings are not merged across YAML documents.

## Keys

`request.message` and `request.messages` match whole messages, `request.body` takes a matcher of the request's JSON, and `request.custom` lists matchers of the whole call, as `Matches` does. `response` takes `message`, `messages`, `body_file` (a JSON file of a message or a list of them), `template`, `header`, `trailer`, `status` and `message_delay`; `bidi` takes `header`, `trailer`, `on` (rules of `when` and `response`), `otherwise` and `on_end`; `proxy` takes `target`, `header` and `remove_headers`. `response_sequence` takes `responses`, a list of `response`s, and `after_ended`, a `response`; `response_random` takes `responses` and `seed`. `delay`, `repeat`, `scenario` and `on_complete` work as in HTTP mock files. `Setup().Matchers(registry)` gives a server the custom matchers its mock files name, as it does an HTTP one: see [Mock files](/http/mock-files).
