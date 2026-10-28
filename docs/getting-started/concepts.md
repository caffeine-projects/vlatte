# Concepts

## Servers

A mock server is a real server: `mockhttp.Server` for HTTP, `mockgrpc.Server` for gRPC. A test starts one, adds mocks, and sends it requests through the code under test. `Setup()` configures it, and its settings can also come from a `.vlatterc` file and `VLATTE_*` variables: see [Configuration](/run/configuration).

## Mocks

A mock is three things:

1. **Matchers** that say which requests it answers: a method, a path, a header, a field of the body. See [HTTP matching](/http/matching), [gRPC matching](/grpc/matching), and the [matchers](/matchers/) both share.
2. **A reply** that says how it answers: a status, headers, a body, a stream of messages, a proxy to a real service. See [HTTP replies](/http/replies) and [gRPC replies](/grpc/replies).
3. **Mock features** that both protocols share: an ID and a name, a priority, `Times` and `Once`, delays, [scenarios](/features/scenarios), and [events such as webhooks](/features/webhooks-events). See [Mock lifecycle](/features/mock-lifecycle).

```go
m.MustMock(mockhttp.Postf("/orders").            // matchers
	Header("Content-Type", match.Equal("application/json")).
	Priority(1).Times(2).                        // mock features
	Reply(mockhttp.Created().JSON(map[string]any{"id": 1}))) // reply
```

When several mocks match a request, the one with the lowest priority number answers.

## Mock files

Mocks can also live in YAML or JSON files, with the same parts as keys: `request`, `response`, `priority`, `scenario`, `on_complete`. A server loads them on start from its work directory, and the `vlatte` command serves them. See [HTTP mock files](/http/mock-files) and [gRPC mock files](/grpc/mock-files).

## The work directory

A server keeps its own files under `testdata/vlatte`: mock files in `_mocks`, recordings in `_mocks_recorded`, and the CLI's admin mocks and journal. See [Work directory](/run/configuration#work-directory).

## When nothing matches

A server answers a request no mock matched itself, in a way that never looks like a mock's answer: HTTP `418` with a `VLatte-Error` header, gRPC `NotFound`. It lists the mocks that came closest. See [HTTP troubleshooting](/http/troubleshooting) and [gRPC troubleshooting](/grpc/troubleshooting).
