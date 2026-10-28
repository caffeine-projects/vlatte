# Installation

Requires Go 1.27 or later.

## The library

```sh
go get github.com/caffeine-projects/vlatte
```

Import the packages you use: `mockhttp` for HTTP, `mockgrpc` for gRPC, and `match` for matchers.

| Package | What it's for |
|---|---|
| `mockhttp` | HTTP mock servers |
| `mockgrpc` | gRPC mock servers |
| `match` | Request matchers, such as `match.Equal`, `match.JSONPath` and `match.All` |
| `match/matchbuild` | The matcher lists of mock files, and the registry of custom matchers |
| `match/matchscenario` | Scenario state for mock servers |
| `webhook` | Calling a URL after a mock is served |
| `fault` | Steps that make an HTTP mock answer wrong |
| `journal` | The request journal |
| `admin` | The admin API |
| `recipe/<protocol>/<recipe>` | Recipes that prepare a server, such as `recipe/http/proxyall` |
| `vlatte` | The types every mock server shares, and the Go template engine |

## The CLI

```sh
go install github.com/caffeine-projects/vlatte/cmd/vlatte@latest
```

Or build the Docker image from the repository's `Dockerfile`: see [Docker](/run/docker).

## Editor support for mock files

Mock files have JSON Schemas at stable addresses. Point your editor at them to get completion and validation: see [Mock file schemas](/reference/schemas).
