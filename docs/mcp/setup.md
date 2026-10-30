# MCP setup

Connect an agent to vlatte over stdio or Streamable HTTP, from the CLI or from Go. What an agent can do with it is in the [MCP overview](/mcp/), and every tool is in [MCP tools](/mcp/tools).

With `admin.MCP()`, the admin API serves its operations to AI agents too, over the [Model Context Protocol](https://modelcontextprotocol.io): a tool per operation, named after it, such as `add_mock`, `list_mocks` and `query_requests`; the streams, the OpenAPI document and the pages have none. A tool's arguments carry what its route takes, the body under a key, such as `{"mock": ...}` for `add_mock` and `{"query": ...}` for `query_requests`, and its tool's schema names them. A tool answers what its route answers, or its problem document as the tool's error. An agent sees only the tools the server can do: a gRPC server has no `mock_schema`, and one without a journal no `query_requests`. `verify_requests` [checks the requests](/verify/admin-api#mcp) as a Go test does, with the check under `check`.

## stdio

For an agent that starts the mock server itself, configure it to launch `vlatte mcp`. Install the [CLI](/getting-started/installation#the-cli) first, and run it from the project directory that holds the mock files:

```json
{
  "mcpServers": {
    "vlatte": {
      "command": "vlatte",
      "args": ["mcp", "--mock-files=mocks/*.yaml"]
    }
  }
}
```

The command takes the server's flags and writes its banner and logs to stderr. MCP over stdio needs no credentials: whoever holds the pipes started the program.

From an error-returning Go function with a context `ctx`, serve the protocol on stdin and stdout:

```go
api := admin.MustNew(admin.MCP())
m := mockhttp.MustNew(mockhttp.Setup().Admin(api).Journal("").LogOutput(os.Stderr))
m.MustStart()
defer m.Close()

if err := api.ServeMCP(ctx, os.Stdin, os.Stdout); err != nil {
	return err
}
```

`ServeMCP` runs until stdin ends or the context is canceled. Only MCP writes to stdout: send your program's logs and diagnostics to stderr too.

## Streamable HTTP

For an agent connecting to an existing server, start the command with known addresses:

```sh
vlatte --addr=127.0.0.1:8080 --admin-addr=127.0.0.1:8081 --mock-files='mocks/*.yaml'
```

Configure the MCP client's Streamable HTTP connection with `http://127.0.0.1:8081/mcp`. This is the admin address; `http://127.0.0.1:8080` serves the mocked API your application calls. The client configuration format depends on the agent.

In a Go program or test, the server's owner enables MCP and keeps the server alive for the agent's work:

```go
api := admin.MustNew(admin.MCP())
m := mockhttp.MustNew(mockhttp.Setup().Admin(api).Journal(""))
m.MustStart()
defer m.Close()

fmt.Println(m.AdminURL() + "/mcp")
// ... run the application or test while the agent connects to this URL ...
```

`POST /mcp` keeps no sessions and answers in JSON. It needs the credentials the rest of the admin API needs; for bearer authentication, the MCP client sends `Authorization: Bearer <token>`. See [Safety](/admin-api/#safety).

## Available tools and disabling MCP

The CLI serves MCP on the admin API by default. In Go, `admin.MCP()` enables it. The examples above also enable a [journal](/more/journal), which makes the request inspection and verification tools available.

`mcp: false` (`--mcp=false`, `VLATTE_MCP=false`) turns MCP off in the CLI. The [tool reference](/mcp/tools) lists which tools each server offers, and the [workflow](/mcp/#inspect-add-and-verify) shows how to use them together.
