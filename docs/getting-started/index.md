# Introduction

VLatte mocks HTTP and gRPC APIs for tests and local development. Use it as a library inside Go tests, or run it as a standalone server for an app in any language, including web and mobile clients.

Mocks match requests, return fixed or templated answers, simulate network failures, and record real services for later replay. You can check what your app sent through the request journal, and change a running server's mocks through its admin API or coding-agent tools.

## Two ways to use it

| | In Go tests | With the `vlatte` CLI |
|---|---|---|
| Starts | `mockhttp.MustNew()` or `mockgrpc.MustNew()` in the test | `vlatte`, a binary or a [Docker image](/run/docker) |
| Mocks | Built in Go, or loaded from [mock files](/http/mock-files) | [Mock files](/http/mock-files), and the [admin API](/admin-api/) |
| Fits | Unit and integration tests of Go code | Any language, local development, demos, CI environments |

Both modes load the same mock files and share the matching, reply, recording and verification features. Go also supports callbacks and custom reply functions that a declarative file cannot hold.

## Choose your first workflow

- **Go tests:** [Install the library](/getting-started/installation#the-library), then write an [HTTP or gRPC test](/getting-started/quick-start#http).
- **Web, mobile or another language:** run the [standalone server](/getting-started/quick-start#the-cli), or follow [Testing a frontend client](/guides/testing-a-frontend-client). A binary or Docker image runs without a Go installation.

## Where to go next

- [Install](/getting-started/installation) the library and the CLI.
- Write a first test in the [quick start](/getting-started/quick-start).
- Learn the few ideas everything else builds on in [Concepts](/getting-started/concepts).
- See where a server's [settings](/getting-started/settings) come from: code, a `.vlatterc` file, `VLATTE_*` variables and flags.
- Then read the section of your protocol, [HTTP](/http/) or [gRPC](/grpc/), and then the sections of the features both share, such as [Matchers](/matchers/), [Templating](/templating/) and [Scenarios](/scenarios/).
