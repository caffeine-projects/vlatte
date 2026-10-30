# Coding Agents

Use a coding agent to write vlatte tests and mock files, or connect it to a running mock server to inspect and change what it serves. The documentation and the server's MCP tools support different parts of that work.

| You want the agent to… | Start with |
|---|---|
| Learn the API and write tests or mock files | [Documentation for agents](/agents/documentation) |
| Inspect mocks, add answers and check requests on a running server | [MCP overview](/mcp/) |
| Connect through stdio or Streamable HTTP | [MCP setup](/mcp/setup) |
| Find a server tool and its arguments | [MCP tools](/mcp/tools) |

## Give the agent the documentation

Start with [`llms.txt`](/llms.txt), the index of documentation pages with links to plain Markdown. Have the agent fetch the pages for its task. [`llms-full.txt`](/llms-full.txt) holds the full reference when it needs broader context.

For example:

```text
Read https://vlatte.dev/llms.txt and the pages relevant to HTTP mocking.
Add a test for this client's retry behavior using vlatte. Follow this
project's existing test conventions, create the mock server in the test,
and check both the result and the requests the client sent.
```

The [documentation page](/agents/documentation) lists the Markdown URLs, schemas and OpenAPI document, with a short instruction you can put in your own project.

## Let the agent use a running server

The `vlatte` command serves its [admin API](/admin-api/) operations over MCP. An agent can inspect the mock schema, validate and add mocks, record a service, move scenarios, and verify the requests your application sent. It can save the mocks as files for later runs.

For a local agent, configure it to start the command:

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

Install the [CLI](/getting-started/installation#the-cli) first. The paths are relative to the command's working directory: point your agent's configuration at the project that holds the mock files. [MCP setup](/mcp/setup) also covers connecting to an existing server over HTTP.

Try the [inspect, add and verify workflow](/mcp/#inspect-add-and-verify). MCP operates on the server; the documentation files above teach the agent how to use vlatte in your code.

To test an application that calls a remote MCP server, see [Mocking a remote MCP server](/guides/mcp-server). That guide uses vlatte to play the upstream server your application calls.
