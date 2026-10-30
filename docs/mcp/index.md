# MCP overview

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

Every tool is listed in [MCP tools](/mcp/tools). For documentation you can give an agent before it writes code, see [Coding Agents](/agents/).

To mock an MCP server that your code calls, rather than drive vlatte from an agent, see [Mocking a remote MCP server](/guides/mcp-server).

## Connect an agent

The `vlatte` command serves MCP two ways, and so does a server built in Go with `admin.MCP()`:

- **stdio**: the agent starts `vlatte mcp` as its MCP server, and talks to it on stdin and stdout.
- **Streamable HTTP**: the agent calls `POST /mcp` on the admin API of a server that runs already, in a test, a container or another terminal.

```json
{"mcpServers": {"vlatte": {"command": "vlatte", "args": ["mcp", "--mock-files=mocks/*.yaml"]}}}
```

[MCP setup](/mcp/setup) shows both, from the CLI and from Go, and how to turn MCP off.

## Inspect, add and verify

Once the agent is connected, this workflow adds an HTTP mock, exercises it and checks the request. The CLI keeps a journal by default; a Go server needs `Setup().Journal("")` to offer the request tools.

1. Call `health` to find the mock server's URL, and `list_mocks` to inspect what it already serves. Call `mock_schema` for the HTTP mock file schema.
2. Call `validate_mock` with the arguments below, then `add_mock` with the same arguments once validation succeeds:

```json
{
  "mock": {
    "id": "agent-hello",
    "request": {"method": "GET", "path": "/hello"},
    "response": {"status": 200, "body": "hello"}
  }
}
```

An ID already in use is refused. Inspect that mock before choosing a new ID or replacing it with `replace_mock`.

3. Call `list_requests` with `{"limit": 0}` and remember its `mark`. This keeps earlier traffic out of the check.
4. Point the application at the mock URL returned by `health`, or exercise the mock directly:

```sh
curl '<mock-url>/hello'
# hello
```

5. Call `verify_requests` with the check below, replacing `41` with the mark from step 3:

```json
{"check": {"mock_id": "agent-hello", "after": 41, "count": 1, "within": "2s"}}
```

Read `passed` in the verdict. A failed check returns a verdict too, with a report and requests that came closest; `get_request` reads a request in detail. See [Verifying over MCP](/verify/admin-api#mcp).

6. To keep the mocks added through the admin API or MCP for later runs, call `save_mocks` with `{"dir": "mocks", "format": "yaml"}`. It returns the paths it wrote, in the directory the setup command's `--mock-files=mocks/*.yaml` pattern loads. Review those files before committing them. [Mock files and the CLI](/guides/mock-files-and-the-cli) shows how to serve and reload them.

## What an agent sees

A tool is an admin API operation, named after it, and answers what its route answers, or its problem document as the tool's error. An agent sees only the tools the server can do: a gRPC server has no `mock_schema`, and one without a journal no `query_requests`. Tools that only read say so, and tools that remove mocks or requests are marked destructive, so an agent can ask before it calls them.

Over HTTP, MCP needs the credentials the rest of the admin API needs: see [Safety](/admin-api/#safety). Over stdio it needs none: whoever holds the pipes started the program.
