# Features

These features work the same for every protocol. Each page shows HTTP first, and says where gRPC differs.

| Feature | What it does |
|---|---|
| [Scenarios](/features/scenarios) | Mocks that answer in turn, as a scenario moves from state to state. |
| [Mock lifecycle](/features/mock-lifecycle) | IDs, names, priorities, `Times`, delays, enabling and disabling. |
| [Templates](/features/templates) | Replies rendered from the request. |
| [Webhooks and events](/features/webhooks-events) | Requests and callbacks a mock runs once it replied. |
| [Request journal](/features/journal) | The requests a server got, to verify after the fact. |
| [TLS](/features/tls) | HTTPS and gRPC over TLS, mutual TLS included, in one line. |

Four features have sections of their own:

- **[Matchers](/matchers/)**: the `match` catalog every protocol shares, from equality and text to JSON, XML, dates and combinations, plus matchers of your own.
- **[Recording](/recording/)**: record a real service once as mock files, and replay it without the service.
- **[Verifying requests](/verify/)**: check which requests the code under test sent, how many, by when and in what order, with the closest requests when a check fails.
- **[Recipes](/recipes/)**: reusable preparations of a server, such as proxying what no mock matches.

Running a server outside Go tests, with the CLI, Docker, the admin API and MCP, is under [Run](/run/).
