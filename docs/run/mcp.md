# MCP

With `admin.MCP()`, the admin API serves its operations to AI agents too, over the [Model Context Protocol](https://modelcontextprotocol.io): a tool per operation, named after it, such as `add_mock`, `list_mocks` and `query_requests`; the streams, the OpenAPI document and the pages have none. A tool's arguments carry what its route takes, the body under a key, such as `{"mock": ...}` for `add_mock` and `{"query": ...}` for `query_requests`, and its tool's schema names them. A tool answers what its route answers, or its problem document as the tool's error. An agent sees only the tools the server can do: a gRPC server has no `add_mock`, and one without a journal no `query_requests`.

```go
api := admin.MustNew(admin.MCP())
m := mockhttp.MustNew(mockhttp.Setup().Admin(api).LogOutput(os.Stderr)) // over stdio, only MCP writes to stdout
m.MustStart()
fmt.Println(m.AdminURL() + "/mcp") // Streamable HTTP, for an agent that runs elsewhere
err := api.ServeMCP(ctx, os.Stdin, os.Stdout) // stdio, for an agent that starts the program, until stdin ends
```

`POST /mcp` keeps no sessions and answers in JSON. It needs the credentials the rest of the admin API needs, which an MCP client sends as `Authorization: Bearer <token>`: see [Safety](/run/admin-api#safety). MCP over stdio needs none: whoever holds the pipes started the program.

The `vlatte` command serves MCP at `POST /mcp` by default, and `vlatte mcp` serves it on stdin and stdout, for an agent that starts the command as its MCP server. `vlatte mcp` takes the server's flags, and writes its banner and logs to stderr. `mcp: false` (`--mcp=false`, `VLATTE_MCP=false`) turns MCP off. An agent's MCP settings start it as any command:

```json
{"mcpServers": {"vlatte": {"command": "vlatte", "args": ["mcp", "--mock-files=mocks/*.yaml"]}}}
```
