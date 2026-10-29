# Introduction

VLatte is a mocking framework for Go. In a test, `mockhttp` and `mockgrpc` start real HTTP and gRPC servers that answer the way the test says: match a request on its method, path, headers, query or body, reply with a status, headers and a templated body, call a webhook, or forward what nothing matches to a real upstream.

The `vlatte` CLI serves HTTP and gRPC mocks from YAML or JSON files, records what passes through it and plays it back, and takes new mocks over an admin API.

## Two ways to use it

| | In Go tests | With the `vlatte` CLI |
|---|---|---|
| Starts | `mockhttp.MustNew()` or `mockgrpc.MustNew()` in the test | `vlatte`, a binary or a [Docker image](/run/docker) |
| Mocks | Built in Go, or loaded from [mock files](/http/mock-files) | [Mock files](/http/mock-files), and the [admin API](/admin-api/) |
| Fits | Unit and integration tests of Go code | Any language, local development, demos, CI environments |

Both share one engine: everything a mock file says, Go can say too, and the other way round for what a file can hold.

## Where to go next

- [Install](/getting-started/installation) the library and the CLI.
- Write a first test in the [quick start](/getting-started/quick-start).
- Learn the few ideas everything else builds on in [Concepts](/getting-started/concepts).
- Then read the section of your protocol, [HTTP](/http/) or [gRPC](/grpc/), and then the sections of the features both share, such as [Matchers](/matchers/), [Templating](/templating/) and [Scenarios](/scenarios/).
