# Features

These features work the same for every protocol. Each page shows HTTP first, and says where gRPC differs.

| Feature | What it does |
|---|---|
| [Matchers](/features/matchers) | The `match` catalog: equality, text, numbers, lists, JSON, XML, dates, and combinations. |
| [Custom matchers](/features/custom-matchers) | Matchers of your own, in Go and in mock files. |
| [Scenarios](/features/scenarios) | Mocks that answer in turn, as a scenario moves from state to state. |
| [Mock lifecycle](/features/mock-lifecycle) | IDs, names, priorities, `Times`, delays, enabling and disabling. |
| [Templates](/features/templates) | Replies rendered from the request. |
| [Webhooks and events](/features/webhooks-events) | Requests and callbacks a mock runs once it replied. |
| [Request journal](/features/journal) | The requests a server got, to verify after the fact. |
| [Recipes](/features/recipes) | Reusable preparations of a server. |

Running a server outside Go tests, with the CLI, Docker, the admin API and MCP, is under [Run](/run/cli).
