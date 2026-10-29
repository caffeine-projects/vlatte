# Run

vlatte runs outside Go tests too: the `vlatte` command serves mock files, for apps in any language, and an admin API changes its mocks while it runs.

| Page | What it covers |
|---|---|
| [CLI](/run/cli) | The `vlatte` command: serving mock files, recording, and the HTTP and gRPC servers it starts. |
| [Configuration](/run/configuration) | Settings from flags, `VLATTE_*` variables and `.vlatterc` files, and the work directory. |
| [Docker](/run/docker) | The image of the command, and running it with your mock files. |
| [Admin API](/run/admin-api) | Changing mocks, scenarios and recordings over HTTP while a server runs, and securing it. |
| [MCP](/run/mcp) | The admin API's operations as tools for AI agents. |

Every setting is listed in the [settings reference](/reference/settings).
