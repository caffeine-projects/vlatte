# Documentation for agents

vlatte publishes plain Markdown documentation and machine-readable API schemas. Start with the index, then fetch the pages needed for the task rather than loading the full reference for every change.

## Choose a resource

| Resource | Use it for |
|---|---|
| [`llms.txt`](/llms.txt) | Find pages by topic, with links to their Markdown copies. |
| [`llms-full.txt`](/llms-full.txt) | Read the documentation in one file, with each page's source URL. |
| Individual Markdown pages, such as [`http/matching.md`](https://vlatte.dev/http/matching.md) | Read a focused reference without the website navigation. |
| [HTTP mock file schema](/schemas/http.schema.json) | Complete and check the structure of HTTP mocks. |
| [gRPC mock file schema](/schemas/grpc.schema.json) | Complete and check the structure of gRPC mocks. |
| [Admin API OpenAPI document](/openapi/admin.yaml) | Find REST operations, parameters and response formats. |
| [Go API](/reference/go-api) | Read exported package contracts on pkg.go.dev. |

```sh
curl -fsSL https://vlatte.dev/llms.txt
curl -fsSL https://vlatte.dev/getting-started/quick-start.md
curl -fsSL https://vlatte.dev/http/matching.md
```

Follow the Markdown URLs in the index: a section's overview is normally `index.md`, such as `/http/index.md`. Code snippets included from the repository are expanded in the Markdown copies too. The site also publishes [`robots.txt`](/robots.txt) and [`ai.txt`](/ai.txt) for crawler access; use `llms.txt` to find the documentation.

## Give the agent a task

| Task | Useful pages |
|---|---|
| Write a Go HTTP client test | [Quick start](/getting-started/quick-start), [REST client guide](/guides/testing-a-rest-client), [Verifying requests](/verify/) |
| Serve mocks for a browser or mobile app | [Frontend client guide](/guides/testing-a-frontend-client), [Mock files and the CLI](/guides/mock-files-and-the-cli), [Docker](/run/docker) |
| Test a gRPC client | [gRPC overview](/grpc/), [Streaming client guide](/guides/grpc-streaming-client) |
| Capture an upstream and replay it | [Record and replay guide](/guides/record-and-replay), [Files and secrets](/recording/files-secrets) |
| Work with mocks on a running server | [MCP setup](/mcp/setup), [MCP tools](/mcp/tools) |

Ask the agent to inspect the project's dependency version and test conventions before adding code. When using the Go library, the installed version's exported API can be checked with `go doc`; the documentation on this site may describe a newer revision than the version your project uses.

## Project instructions

You can add an instruction like this to your own project's `AGENTS.md`, or put it directly in a prompt:

```text
For vlatte tests and mock files, start from https://vlatte.dev/llms.txt
and read the Markdown pages relevant to the task. Check the installed
vlatte version and follow this project's test conventions. Run the tests
you change. For mock files, use the published JSON Schema and check that
the files load in the vlatte version this project runs.
```

The schemas check file structure. A file can still fail to load if it names a missing body file, descriptor or other runtime resource: see [Mock file schemas](/reference/schemas).
