# Mock the GitHub MCP server

VLatte plays [GitHub's MCP server](https://github.com/github/github-mcp-server) with two of its tools, `get_me` and `issue_read`, so an MCP client can be tested without GitHub. It uses VLatte's MCP mocks: an endpoint mock answers what every MCP server answers, and a mock per tool answers that tool's calls. The MCP server is one route, `/mcp/`, beside a REST route, `/api/user`, on the same server.

[github.mock.yaml](testdata/github.mock.yaml) holds these mocks:

| Mock | Answers |
|---|---|
| `github`, the endpoint mock | `initialize`, `ping`, notifications (`202`), `tools/list` with the tools of the mocks below, `GET /mcp/` with `405`, and other methods with error `-32601`, so a client that first tries `server/discover` falls back to `initialize` |
| `github-get-me` | A call of `get_me` with an `Authorization: Bearer …` header: the profile, as JSON text |
| `github-issue-read` | A call of `issue_read` for issue 42 of `octocat/hello-world`, with the same header: the issue, as JSON text |
| `github-rest-user` | `GET /api/user`: the profile, as a REST API answers it |

A tool call that no tool mock matches, such as one without the bearer token, gets error `-32602`, whose data lists the mocks that came closest and why they didn't match.

## In Go tests

```sh
go -C examples test ./05-mcp-github/...
```

[github_test.go](github_test.go) connects the [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) client, lists the tools and calls both. `TestGoMocksAnswerAnMCPClientAsGitHubDoes` builds the mocks in Go. This is the `get_me` mock, where `_endpoint` is `/mcp/` and `_user` is the profile map:

```go
mockhttp.MCP(_endpoint).ID("github-get-me").MCPTool("get_me", "Get my user profile").
	Header("Authorization", match.HasPrefix("Bearer ")).
	Reply(mockhttp.MCPJSON(_user)),
```

`TestMockFileAnswersAnMCPClientAsGitHubDoes` loads the same mocks from the file instead, with `mockhttp.Setup().MockFilePatterns("testdata/github.mock.yaml")`. Both also check the REST route.

## From a file, with the CLI

From the repository root:

```sh
make example:mcp-github
```

Then point any MCP client that speaks Streamable HTTP at `http://127.0.0.1:8080/mcp/`, or call a tool by hand:

```sh
curl -s http://127.0.0.1:8080/mcp/ -H 'Authorization: Bearer x' -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_me","arguments":{}}}'
```

```json
{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"html_url\":\"https://github.com/octocat\",\"id\":1,\"login\":\"octocat\",\"name\":\"The Octocat\"}"}]}}
```

## Mock your own MCP server

Copy `github.mock.yaml`. In the endpoint document, change the server's name and version. Then write one document per tool: its `mcp.tool` names the tool and describes its input, and its `mcp_result` is what a call answers. The endpoint mock lists the tools on its own.
