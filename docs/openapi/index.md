# OpenAPI

`vlatte openapi` turns an OpenAPI spec into [mock files](/http/mock-files) the `vlatte` command serves: a mock API that answers every operation of the spec, with no code to write. It reads OpenAPI 3.0, 3.1 and 3.2, in JSON or YAML, from a file or an `http(s)` URL.

```sh
vlatte openapi --in petstore.yaml --out testdata/vlatte/_mocks
vlatte --addr=127.0.0.1:8080
curl http://127.0.0.1:8080/pets/3fa85f64-5717-4562-b3fc-2c963f66afa6
# {"ageMonths":30,"breed":"meadow cedar","color":"amber",...}
```

`testdata/vlatte/_mocks` is the folder `vlatte` serves by default, so the second command needs no flags.

## What you get

For each operation of the spec, the mocks:

- **Reply** with each response the spec declares, in each of its media types: the spec's examples, or made-up values that fit the schemas, the same on every run. See [Replies](/openapi/replies).
- **Refuse requests that break the spec,** as the real server would: `401` without the credential the operation asks for, `415` for a body in a media type it doesn't take, `400` or `422` for parameters or a body that don't fit their schemas, and `405` for a method the path doesn't have. See [Requests that break the spec](/openapi/contract).
- **Negotiate content:** a client gets the media type its `Accept` header prefers, or a `406` when the operation has none it takes.

Everything else vlatte does works on top: [verify](/verify/) what a client sent, add [faults](/http/faults) or [delays](/more/mock-lifecycle#delays), and change mocks while the server runs through the [admin API](/admin-api/).

## When to use it

Start from the spec when you have one, and the mocks cover every operation from the first run. Then write mocks by hand only for what a test needs that the spec can't say, such as a particular answer for one ID: [hand-written mocks win](/openapi/replies#changing-a-reply) over generated ones. To mock a service from what it actually answers instead, [record](/recording/) it.

## Next steps

- [Generating mocks](/openapi/generate): the command, the files it writes, and running it again when the spec changes.
- [Replies](/openapi/replies): where bodies come from, which mocks are on, and how to change one.
- [Requests that break the spec](/openapi/contract): the errors the mocks answer, and the `openapi` matcher.
- [Troubleshooting](/openapi/troubleshooting): warnings, mocks written disabled, and large specs.
