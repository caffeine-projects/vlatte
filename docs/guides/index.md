# Guides

Each guide works through one task from start to end, with code you can run. The sections on [HTTP](/http/), [gRPC](/grpc/) and each feature say what each part does; the guides show the parts together.

| Guide | What you build |
|---|---|
| [Testing a frontend client](/guides/testing-a-frontend-client) | A mock API for a browser app without Go, with CORS, request verification and mobile connection notes. |
| [Testing a REST client](/guides/testing-a-rest-client) | Tests of a Go client of a users API: the happy path, an error answer, and what the client sent. |
| [Mock files and the CLI](/guides/mock-files-and-the-cli) | Mock files with editor support, served by the `vlatte` command and loaded by Go tests. |
| [Record and replay](/guides/record-and-replay) | Mock files recorded from a real API once and replayed in every later run. |
| [Chaos testing](/guides/chaos-testing) | Tests that a client retries a failing server, survives a reset connection, and gives up on a slow one. |
| [Stateful flows](/guides/stateful-flows) | A cart and checkout flow whose answers depend on what came before, tested in parallel. |
| [Testing a gRPC streaming client](/guides/grpc-streaming-client) | Tests of a client of a server stream, through errors and a server that dies midway. |
| [Docker Compose](/guides/docker-compose) | vlatte next to an app under test, with mocks changed over the admin API. |
| [Testing webhooks](/guides/webhooks) | A payment API that calls your service back, and a test that the callback came. |
| [HTTPS and mTLS mocks](/guides/https-mtls) | Tests of a client of an HTTPS-only API, with mutual TLS, and the CLI serving the same mock. |
| [Testing an LLM streaming client](/guides/llm-streaming) | Tests of a client of a chat API that streams its answers, through a dropped stream and a rate limit. |
| [Mocking a remote MCP server](/guides/mcp-server) | Mock files that play an MCP server over Streamable HTTP, and a test of an MCP client against them. |
