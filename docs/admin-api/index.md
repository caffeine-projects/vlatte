# Admin API

The admin API manages a server while it runs. It is an HTTP server of its own, on its own address, the same for mock servers of every protocol, and it leaves every path of the mock server to the mocks. It serves only when asked, from the `admin` package:

```go
m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew(admin.Addr("127.0.0.1:8081")))) // no Addr picks a random local port
m.MustStart()
fmt.Println(m.AdminURL()) // http://127.0.0.1:8081
```

A gRPC server serves the same admin API with `mockgrpc.Setup().Admin(admin.MustNew(...))`, with gRPC mock files, and recordings with the keys of its `grpc.record` section. An `Admin` serves one mock server. A server that doesn't ask for an admin API links neither it nor what it serves with.

`admin.Use(middlewares...)` wraps every HTTP request of the admin API, `POST /mcp` included, in the middlewares given, the first outermost, before the credentials are checked, so one can answer a CORS preflight, which carries none. MCP over stdio doesn't go through them.

## Authentication

`admin.BasicAuth(username, password)` and `admin.BearerToken(token)` ask every HTTP request of the admin API for credentials: the pages, `/openapi`, the streams, `POST /mcp` and unknown paths too. With both, a request passes with either. A request without them gets `401` with the code `unauthorized`, and `WWW-Authenticate` names the schemes taken. Only the Kubernetes probes, `/livez`, `/readyz` and `/startupz`, need none, as a probe that needs a secret turns a wrong one into a restart loop: they answer their status alone then, and a `503` that doesn't say why, which the server logs. `/health`, which says what the server serves, needs them.

```go
api := admin.MustNew(
	admin.Addr(":8081"),
	admin.BasicAuth("ops", os.Getenv("ADMIN_PASSWORD")),
	admin.BearerToken(os.Getenv("ADMIN_TOKEN")),
	admin.TLS(&tls.Config{Certificates: []tls.Certificate{cert}}),
)
```

A browser shows its sign-in prompt for the admin pages, and sends what it was given with each of their requests and live streams. It sends no bearer token, so the prompt takes the token as the password, with any username. Basic has no sign-out: closing the browser forgets the credentials. A token goes in `Authorization: Bearer`, never in a query parameter. Credentials are compared in constant time, and `New` refuses empty ones, a username with `:`, control characters, and a token an `Authorization` header can't carry; its errors never quote them. Generate a token with `openssl rand -hex 32`.

Basic and bearer credentials travel in plain text over HTTP: `admin.TLS(config)` serves HTTPS, and `AdminURL` starts with `https://`. The admin API logs a warning when it listens beyond the local host without credentials, or with credentials over plain HTTP.

## In the vlatte command

The `vlatte` command serves the admin API by default: `admin: false` (`--admin=false`, `VLATTE_ADMIN=false`) turns it off, and `admin.addr` (`--admin-addr`, `VLATTE_ADMIN_ADDR`) sets its address. Its gRPC server serves an admin API of its own, which `grpc.admin: false` (`--grpc-admin=false`) turns off and `grpc.admin.addr` (`--grpc-admin-addr`) places. These settings are the command's: a server made in Go ignores them, even in a `.vlatterc` it shares with the command, and serves an admin API only when its code asks for one.

The command's credentials and certificate apply to both admin APIs. `admin.auth.username` with `admin.auth.password` asks for Basic, and `admin.auth.token` for a bearer token. The password and the token have no flag, as the command line shows to every user of the host: set them in the environment (`VLATTE_ADMIN_AUTH_PASSWORD`, `VLATTE_ADMIN_AUTH_TOKEN`), in a config file, or in a file that `admin.auth.password_file` or `admin.auth.token_file` names, such as a Docker or Kubernetes secret, whose trailing line break isn't part of it. `admin.tls.cert` and `admin.tls.key` (`--admin-tls-cert`, `--admin-tls-key`) serve HTTPS. `vlatte health` reads the same settings: with a certificate, it asks the admin API over HTTPS, and trusts that certificate alone.

```sh
docker run --rm -p 8080:8080 -p 8081:8081 \
  -v "$PWD/secrets:/run/secrets:ro" -v "$PWD/certs:/certs:ro" \
  -e VLATTE_ADMIN_AUTH_TOKEN_FILE=/run/secrets/admin_token \
  -e VLATTE_ADMIN_TLS_CERT=/certs/admin.pem -e VLATTE_ADMIN_TLS_KEY=/certs/admin-key.pem \
  ghcr.io/caffeine-projects/vlatte:v1.0.0
```

See [Docker](/run/docker) for the image's ports and settings.

## Routes

The [OpenAPI reference](/reference/admin-api.html){target="_blank" rel="noopener noreferrer"} describes every route with its parameters, bodies and answers. Its document is at [`https://vlatte.dev/openapi/admin.yaml`](/openapi/admin.yaml){target="_self"}, for client generators and API tools, and a running server serves it at `GET /openapi`.

| Route | What it does |
|---|---|
| `GET /health` | Answers once the admin API serves, with the kind of the server and the URL of its mocks. |
| `GET /livez` | The Kubernetes liveness probe: answers as `/health` does, whatever the mock store says. Needs no credentials, and answers its status alone when the admin API asks for some. |
| `GET /readyz` | The Kubernetes readiness probe: answers as `/health` does once the mock store's `Health` returns nil, and `503` while it returns an error. Needs no credentials, as `/livez`. |
| `GET /startupz` | The Kubernetes startup probe: answers as `/livez` does. The admin API serves only once the mock server loaded its mocks. |
| `POST /reset` | Removes the mocks added through the admin API, loads the mock files again, saved ones included, starts every scenario over, and clears the [journal](/more/journal). Mocks added in code, and the [key-value store](/templating/#key-value-store), stay as they are. The mocks of a persistent store come back from it: reset never deletes what it holds. |
| `GET /mocks` | Lists every mock, enabled or not, with its `priority`, `enabled`, `hits` and `origin`. |
| `POST /mocks` | Adds the mock of the body, a [mock file](/http/mock-files). An id in use is a `409`. |
| `GET /mocks/{id}` | The mock as a mock file describes it, which `PUT` and `POST` take back. A mock whose reply a mock file can't hold, an `Echo`, `Func` or `Handler`, answers `500` with the code `describe_failed`. A gRPC mock built in Go does too when any part of it is one a mock file can't hold, such as a `Shared` reply, a `CallMatches` matcher, a callback or a `DelayFunc` delay, and the detail names it. |
| `PUT /mocks/{id}` | Replaces the mock, in one step. The body's id must be the path's. |
| `PATCH /mocks/{id}` | Enables or disables the mock: `{"enabled": false}`. |
| `DELETE /mocks/{id}`, `DELETE /mocks` | Removes the mock, or every mock. |
| `POST /mocks/validate` | Checks a mock as `POST /mocks` would, without adding it. |
| `GET /schema` | The [JSON Schema](/reference/schemas) of mock files, for clients that write them. A gRPC server answers `501`. |
| `POST /mocks/reload` | Loads the mock files, and the [persistent store](/admin-api/#persistent-admin-mocks), again, as `m.Reload()` does. A mock file's mock takes the place of a mock added through the admin API with its id. |
| `POST /mocks/save` | Writes the mocks added through the admin API as mock files, `<id>.mock.<format>`, and serves them from the files from then on. `{"dir": ..., "format": ...}` defaults to `{workdir}/_mocks` and YAML, which the default mock file patterns load on the next start; `dir` stays inside `root_dir`. When a mock can't be written as a mock file that loads back, nothing is written, and files that fail to load are put back as they were. |
| `GET /scenarios`, `PUT /scenarios/{name}`, `DELETE /scenarios/{name}`, `DELETE /scenarios` | Lists the state of each scenario, moves one to a state, `{"state": "paid"}`, starts one over, and starts every scenario over. Names ignore case: `Checkout` is `checkout`. `?key=t1` moves or starts over the instance of a key only. |
| `GET /kv`, `GET /kv/{key}`, `PUT /kv/{key}`, `DELETE /kv/{key}` | Lists the keys of the [key-value store](/templating/#key-value-store) templates read, in order, gets the value of a key, sets one, `{"value": "abc123"}`, and removes one. Templates read a change from the next request on. A key is one path segment, so a key with a slash is sent escaped: `/kv/a%2Fb` is `a/b`. Removing a key that isn't set answers `204` too. |
| `POST /kv/reload` | Loads the key-value store again from its CSV file, in place of every value: values set since are gone, and keys removed since come back. A file with a problem changes nothing, and the answer names its line. A store with no file answers `501`. `POST /mocks/reload` and `POST /reset` leave the store as it is. |
| `GET /requests` | Lists the newest requests of the [journal](/more/journal), with their `count` and the journal's `mark`: `?limit=` (100 by default, 1000 at most, `0` for the count alone) and `?before=`, the `next` of a page, page through them, and `?after=`, a mark, leaves out the requests up to it. |
| `QUERY /requests` | Finds and counts the requests a query selects, in the body: see [Request journal](/more/journal). It uses the HTTP [`QUERY`](https://httpwg.org/specs/rfc10008.html) method, safe as `GET` is, with a body: `curl -X QUERY`. |
| `QUERY /requests/verify` | Checks the requests a query selects, their count, over a wait or not, or their order, and answers the verdict: see [Verifying requests](/verify/admin-api). |
| `GET /requests/{id}`, `DELETE /requests` | Gets a request by the id its answer's `VLatte-Request-Id` names, and clears the journal. |
| `GET /events` | Streams what the server serves as it happens, as [Server-Sent Events](https://html.spec.whatwg.org/multipage/server-sent-events.html): requests a mock answered, requests no mock matched, proxied requests, warnings, errors and [scenario](/scenarios/) moves. Nothing is kept: the [journal](/more/journal) keeps the requests. `curl -N <admin>/events` follows it. |
| `GET /logs` | Streams the server's log lines, as it writes them, as Server-Sent Events. A server given its own `Logger` streams none. |
| `POST /recording`, `GET /recording`, `DELETE /recording` | Starts a [recording](/recording/) with the keys of the `record` section, counts its calls, and stops it, listing the files it wrote. |
| `GET /` | The admin's homepage: what the mock server serves and what it served, its mocks, requests, scenarios and recording, live, with links to the other pages. Every page has a navbar with a light and dark theme toggle. |
| `GET /recorder` | A page that records a service from a browser. |
| `GET /scenario-board` | A page that shows and moves [scenarios](/scenarios/) from a browser, live. |
| `POST /mcp` | The [MCP](/mcp/setup) endpoint, when the admin API serves MCP; `501` otherwise. |
| `GET /docs`, `GET /openapi` | A page that renders the OpenAPI document, and the document. The page loads [Scalar](https://scalar.com) from a CDN, so it needs internet: the version vlatte pins, checked against its hash, or a newer one that `admin.DocsVersion("1.80.0")` (`admin.docs_version`, `--admin-docs-version` for the command) names. |

## Formats

Bodies are JSON, or YAML with `Content-Type: application/yaml`. Answers follow `Accept`: JSON, or YAML when it is preferred. A relative `body_file` in a mock sent to the admin API opens from the server's working directory, as `GET /mocks/{id}` writes it.

## Errors

Errors are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem documents with a stable `code`; the problems of a mock are listed one by one:

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

## Safety

Without [credentials](#authentication), anyone who reaches the admin API can change the mocks, which can read files the server can, and read the requests of the journal. It listens on a local address unless told otherwise; with `VLATTE_DOCKER_HOST`, the CLI listens on every interface, as for the mocks, so set credentials and TLS, or publish its port only where the network is trusted. A browser's cross-site request that would change the server, or query its journal, is refused with `403`, credentials or not: a browser sends those of its sign-in prompt on its own.

## Persistent admin mocks

The mocks the admin API adds last as long as the server, unless a persistent store keeps them. Each change the admin API makes, adding, replacing, enabling, disabling or removing a mock, reaches the store at once, and the server serves what the store holds when it starts and on each reload, so a change made to the store directly takes effect then too. The mocks of mock files and code never reach it, and a mock file's mock wins over a mock of the store with its id. A reload swaps the mocks of the store and of the mock files in one step: when either fails, the server serves what it served before.

`FileStore(dir)` keeps them as mock files, `<id>.mock.yaml`, which only the store loads: keep `dir` out of the mock file patterns. A relative `dir` starts at `root_dir`, and `{workdir}` at the [work directory](/run/configuration#work-directory). A mock whose description doesn't load back is refused, as its file would fail every later start. A file's mock keeps the ID its name gives: one whose `id` differs fails the start and the reload, as the store finds its file by that name. The `vlatte` command keeps them in `{workdir}/_admin`: `admin.dir` (`--admin-dir`, `VLATTE_ADMIN_DIR`) sets another directory, and an empty one keeps the mocks in memory. It keeps those of the gRPC server's admin API in `{workdir}/_grpc_admin`, the HTTP server's work directory, as the gRPC server has none: `grpc.admin.dir` (`--grpc-admin-dir`, `VLATTE_GRPC_ADMIN_DIR`) sets another, whose relative paths start at `grpc.root_dir`, and an empty one keeps them in memory. A kept gRPC mock of a method the descriptor sets no longer hold fails the start, which names its file: remove the file to start again. In Go, `AdminStore` needs `Admin`:

```go
m := mockhttp.MustNew(mockhttp.Setup().Admin(admin.MustNew()).AdminStore(mockhttp.FileStore("{workdir}/_admin")))
```

Any `vlatte.Store[*mockhttp.Mock]` can be the store, such as a database collection. It keeps the descriptions of the mocks by ID: `EncodeMock` writes one, and `DecodeMock` builds the mock back for the server. Its `Health(ctx)` returns an error while it can't serve the mocks, such as when the database is down: `GET /readyz` answers `503` until it returns nil.

```go
mockhttp.Setup().AdminStore(func(app *mockhttp.Server) (vlatte.Store[*mockhttp.Mock], error) {
	return &collectionStore{app: app, coll: coll}, nil // Store: EncodeMock(mock, "json"); PickAll: DecodeMock(app, doc, "json")
})
```

A gRPC server takes a store the same way, `mockgrpc.Setup().Admin(admin.MustNew()).AdminStore(mockgrpc.FileStore(dir))`, with `mockgrpc.EncodeMock` and `mockgrpc.DecodeMock` for a store of yours. Its relative `dir` starts at its root directory.

`POST /mocks/save` is apart from the store: it exports the admin API's mocks as mock files to commit, which then serve in place of the store's copies.
