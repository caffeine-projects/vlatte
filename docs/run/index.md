# Run

vlatte runs outside Go tests too: the `vlatte` command serves mock files, for apps in any language.

| Page | What it covers |
|---|---|
| [CLI](/run/cli) | The `vlatte` command: serving mock files, recording, and the HTTP and gRPC servers it starts. |
| [Configuration](/run/configuration) | Settings from flags, `VLATTE_*` variables and `.vlatterc` files, and the work directory. |
| [Docker](/run/docker) | The image of the command, and running it with your mock files. |

Every setting is listed in the [settings reference](/reference/settings). A running server takes new mocks over the [admin API](/admin-api/), and from AI agents over [MCP](/mcp/).
