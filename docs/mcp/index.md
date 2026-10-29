# MCP

vlatte speaks the [Model Context Protocol](https://modelcontextprotocol.io): an AI agent, such as a coding assistant, can drive a mock server with the same operations the [admin API](/admin-api/) serves over REST. It sees every mock, adds and changes them, records a real service, moves scenarios, and reads and checks the requests clients sent.

| An agent can… | With the tools |
|---|---|
| See the mocks the server serves, and the schema of mock files | `list_mocks`, `get_mock`, `mock_schema` |
| Write a mock, check it, and serve it at once | `validate_mock`, `add_mock`, `replace_mock`, `update_mock` |
| Keep the mocks it wrote as mock files | `save_mocks`, `reload_mocks` |
| Record a real service as mock files | `start_recording`, `recording_status`, `stop_recording` |
| Walk a stateful flow | `list_scenarios`, `set_scenario`, `reset_scenario` |
| Find what a client sent, and check it as a test does | `list_requests`, `query_requests`, `verify_requests` |
| Start over | `reset` |

Every tool is listed in [Tools](/mcp/tools).

To mock an MCP server that your code calls, rather than drive vlatte from an agent, see [Mocking a remote MCP server](/guides/mcp-server).

## Connect an agent

The `vlatte` command serves MCP two ways, and so does a server built in Go with `admin.MCP()`:

- **stdio**: the agent starts `vlatte mcp` as its MCP server, and talks to it on stdin and stdout.
- **Streamable HTTP**: the agent calls `POST /mcp` on the admin API of a server that runs already, in a test, a container or another terminal.

```json
{"mcpServers": {"vlatte": {"command": "vlatte", "args": ["mcp", "--mock-files=mocks/*.yaml"]}}}
```

[Setup](/mcp/setup) shows both, from the CLI and from Go, and how to turn MCP off.

## What an agent sees

A tool is an admin API operation, named after it, and answers what its route answers, or its problem document as the tool's error. An agent sees only the tools the server can do: a gRPC server has no `mock_schema`, and one without a journal no `query_requests`. Tools that only read say so, and tools that remove mocks or requests are marked destructive, so an agent can ask before it calls them.

Over HTTP, MCP needs the credentials the rest of the admin API needs: see [Safety](/admin-api/#safety). Over stdio it needs none: whoever holds the pipes started the program.
