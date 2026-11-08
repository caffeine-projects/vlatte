# Requests that break the spec

A request a client should never send gets the error the real server would answer, so you see a client's mistake in a test rather than in production. The mocks check each request against the spec:

| Status | When the request |
|---|---|
| `401` | Lacks the credential the operation's `security` asks for: a header, a query parameter or a cookie of one of its schemes. |
| `415` | Has a body in a media type the operation doesn't take. |
| `400`, or `422` | Has parameters or a body that don't fit their schemas. The mock answers `422` when the operation declares a `422` response and no `400`. |
| `405` | Uses a method the path has no operation for. |

They are checked in that order: a request without its credential gets the `401`, whatever its body.

## What they answer

Each answers the operation's own response for that status, as [Replies](/openapi/replies#bodies) builds it. When the spec declares none, it answers a [problem](https://www.rfc-editor.org/rfc/rfc9457) body, which for a `400` or `422` lists what the request breaks:

```sh
curl -i -X POST http://127.0.0.1:8080/books \
  -H 'Authorization: Bearer token' -H 'Content-Type: application/json' \
  -d '{"title": "Dune", "pages": 0}'
# HTTP/1.1 400 Bad Request
# Content-Type: application/problem+json
#
# {"title":"Bad Request","status":400,"errors":["body at /pages: minimum: got 0, want 1","body: missing property 'isbn'"]}
```

The `401` names the operation's `http` schemes in `WWW-Authenticate`, the `415` names the media types it takes in `Accept`, and the `405` names the methods of the path in `Allow`:

```sh
curl -i -X PATCH http://127.0.0.1:8080/pets
# HTTP/1.1 405 Method Not Allowed
# Allow: GET, HEAD, POST, QUERY
# Content-Type: application/problem+json
#
# {"status":405,"title":"Method Not Allowed"}
```

The mocks check that a credential is there, not that it is valid: any bearer token passes.

## How the mocks check requests

The mocks that refuse requests use the `openapi` matcher, under `not`, with the copy of the spec the command writes beside them:

<<< @/../cmd/vlatte/testdata/openapi/petstore/pets-post-401-json.mock.yaml{yaml}

The matcher takes an object:

| Key | Meaning |
|---|---|
| `spec` | The spec file, relative to the mock file. |
| `operation` | The `operationId` of an operation, or its method and path, such as `GET /pets/{id}`. |
| `check` | What the request must keep: `security`, its credential; `content_type`, a body in a media type the operation takes; or `request`, its parameters and body as the spec describes them. |

It matches a request that keeps that part of the contract. Your own mock files can use it too, such as a mock that answers only valid orders, with the spec copy the command wrote:

```yaml
request:
  method: POST
  path: /orders
  custom:
    - [openapi, {spec: Modern-Petstore.6e8fa666.openapi.yaml, operation: createOrder, check: request}]
response:
  status: 201
```

The name of the copy changes with the spec, so a mock that names it needs updating when the spec changes. The `vlatte` command knows the matcher. A Go server that loads these mock files registers it from the `match/matchopenapi` package, as its [package documentation](https://pkg.go.dev/github.com/caffeine-projects/vlatte/match/matchopenapi) shows.

## Turning a check off

To accept a request the spec refuses, write a mock for it by hand: [hand-written mocks win](/openapi/replies#changing-a-reply) over generated ones, the refusing ones included. To turn a refusing mock off for the whole run, disable it with the admin API, as [Replies](/openapi/replies#which-mocks-are-on) shows.
