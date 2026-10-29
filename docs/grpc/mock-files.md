# Mock files

`Setup().MockFiles("testdata/grpc/*.yaml")` loads gRPC mock files at `Start`, and `m.Reload()` again; `mockgrpc.FromFile` and `FromBytes` build one. Their keys mirror the Go API, and [`mockgrpc/schema/grpc.schema.json`](/reference/schemas) describes them for editors:

```yaml
id: denied
priority: 1
request:
  method: users.Users/Get
  header: {authorization: [hasprefix, "Bearer "]}
  fields: {id: [regex, "^u-"]}
response:
  status:
    code: PERMISSION_DENIED
    message: not yours
    details:
      - {"@type": type.googleapis.com/google.rpc.ErrorInfo, reason: NOT_OWNER}
```

Start a YAML file with `# yaml-language-server: $schema=https://caffeine-projects.github.io/vlatte/schemas/grpc.schema.json` for completion and checks in editors, or give a JSON file a `"$schema"` key: see [Mock file schemas](/reference/schemas).

## Keys

`request.message` and `request.messages` match whole messages, `request.body` takes a matcher of the request's JSON, and `request.custom` lists matchers of the whole call, as `Matches` does. `response` takes `message`, `messages`, `body_file` (a JSON file of a message or a list of them), `template`, `header`, `trailer`, `status` and `message_delay`; `bidi` takes `header`, `trailer`, `on` (rules of `when` and `response`), `otherwise` and `on_end`; `proxy` takes `target`, `header` and `remove_headers`. `response_sequence` takes `responses`, a list of `response`s, and `after_ended`, a `response`; `response_random` takes `responses` and `seed`. `delay`, `repeat`, `scenario` and `on_complete` work as in HTTP mock files. `Setup().Matchers(registry)` gives a server the custom matchers its mock files name, as it does an HTTP one: see [Mock files](/http/mock-files).
