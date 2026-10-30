# Features

Use these features to describe realistic API behavior, reuse mocks across tests and tools, and check what an application sent. HTTP and gRPC share matchers, scenarios, recording and request verification; each page says where the protocols differ.

| Feature | What it helps you test |
|---|---|
| [SSE (Server-sent Events)](/sse/) | Streaming HTTP clients, event timing, reconnects and malformed event data. |
| [Matchers](/matchers/) | Request methods, paths, headers and structured values such as JSON, XML and JWT claims. |
| [Templating](/templating/) | Replies that use values from the request, files or a key-value store. |
| [Scenarios](/scenarios/) | Stateful flows, including separate state for each caller or test key. |
| [Recording](/recording/) | Capture a real HTTP or gRPC service as mock files and replay it without the service. |
| [Verifying requests](/verify/) | Check which requests arrived, how many, by when and in what order. |
| [Webhooks and events](/webhooks/) | Trigger a callback after a mock responds. |
| [TLS](/tls/) | HTTPS, gRPC over TLS and mutual TLS. |
| [Recipes](/recipes/) | Reuse prepared mocks, such as OAuth and OpenID Connect providers. |
| [Mock lifecycle](/more/mock-lifecycle) | IDs, names, priorities, `Times`, delays, enabling and disabling. |
| [Request journal](/more/journal) | The requests a server got, to verify after the fact. |

Start with [HTTP](/http/) or [gRPC](/grpc/) for protocol-specific matching and replies. Use the [admin API](/admin-api/) to manage a running server, or connect a [coding agent](/agents/) over MCP. The [guides](/guides/) combine the features into complete testing tasks.
