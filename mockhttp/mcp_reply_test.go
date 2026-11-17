package mockhttp

import (
	json "encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPResult_EnvelopesEchoTheRequestID(t *testing.T) {
	// The answer goes to the call that asked: its id comes back as sent, a number or a string.
	tests := []struct {
		name   string
		result *MCPResult
		want   string // the answer, with ID for the id
	}{
		{
			name:   "text",
			result: MCPText("hi"),
			want:   `{"jsonrpc":"2.0","id":ID,"result":{"content":[{"type":"text","text":"hi"}]}}`,
		},
		{
			name:   "JSON, its keys in order",
			result: MCPJSON(map[string]any{"b": 2, "a": 1}),
			want:   `{"jsonrpc":"2.0","id":ID,"result":{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}}`,
		},
		{
			name:   "the tool's own error",
			result: MCPToolError("bad"),
			want:   `{"jsonrpc":"2.0","id":ID,"result":{"content":[{"type":"text","text":"bad"}],"isError":true}}`,
		},
		{
			name:   "a JSON-RPC error",
			result: MCPError(-32000, "x"),
			want:   `{"jsonrpc":"2.0","id":ID,"error":{"code":-32000,"message":"x"}}`,
		},
	}

	for _, tt := range tests {
		for _, id := range []string{`7`, `"s"`} {
			t.Run(tt.name+" "+id, func(t *testing.T) {
				m := startMCP(t, nil, MCP("/mcp").MCPTool("t", "").Reply(tt.result))

				res, body := mcpPost(t, m.URL("/mcp"),
					`{"jsonrpc":"2.0","id":`+id+`,"method":"tools/call","params":{"name":"t"}}`)
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, MIMEApplicationJSON, res.Header.Get(HeaderContentType))
				require.Equal(t, strings.Replace(tt.want, "ID", id, 1), body)
			})
		}
	}
}

func TestMCPResult_TemplateReadsArgumentsAndData(t *testing.T) {
	m := startMCP(t, nil, MCP("/mcp").MCPTool("t", "").Reply(
		MCPText(`{{ (fromJSON .Request.Body.String).params.arguments.owner }}-{{ .Extra.x }}`).
			Template().TemplateData(map[string]any{"x": "y"})))

	res, body := mcpPost(t, m.URL("/mcp"), toolCall("t", `{"owner":"octocat"}`))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"octocat-y"}]}}`, body)
}

func TestMCPResult_TemplateOnJSONOrErrorFails(t *testing.T) {
	m := MustNew(Setup().Silent())

	for _, result := range []*MCPResult{MCPJSON(map[string]any{"a": 1}).Template(), MCPError(1, "x").Template()} {
		_, err := m.Mock(MCP("/mcp").MCPTool("t", "").Reply(result))
		require.ErrorIs(t, err, errMCPResultTemplate)
	}
}

func TestMCPResult_JSONThatDoesntEncodeFails(t *testing.T) {
	m := MustNew(Setup().Silent())

	_, err := m.Mock(MCP("/mcp").MCPTool("t", "").Reply(MCPJSON(make(chan int))))
	require.ErrorContains(t, err, "mcp result: encoding the JSON: ")

	_, ok := errors.AsType[*json.SemanticError](err)
	require.True(t, ok, "%v is no JSON encoding error", err)
}

func TestMCPResult_DescribeLoadsBack(t *testing.T) {
	m := MustNew(Setup().Silent())

	for _, result := range []*MCPResult{
		MCPText("hi"),
		MCPJSON(map[string]any{"a": 1}),
		MCPToolError("bad"),
		MCPError(-32000, "x"),
		MCPText("{{ .Extra.x }}").Template().TemplateData(map[string]any{"x": "y"}),
	} {
		mock, err := MCP("/mcp").ID("t").MCPTool("t", "").Reply(result).Build(m)
		require.NoError(t, err)

		desc, err := mock.MarshalJSON()
		require.NoError(t, err)

		back, err := FromBytes(desc, "json").Build(m)
		require.NoError(t, err, string(desc))

		again, err := back.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, string(desc), string(again))
	}
}

func TestMCPResult_DescribeKeepsLargeIntegers(t *testing.T) {
	// A JSON result and an input schema describe their numbers as they are: a saved mock answers the same ids.
	m := MustNew(Setup().Silent())

	mock, err := MCP("/mcp").ID("t").MCPTool("t", "").
		MCPInputSchema(map[string]any{"type": "object", "maxProperties": int64(9007199254740993)}).
		Reply(MCPJSON(map[string]any{"id": int64(9007199254740993)})).Build(m)
	require.NoError(t, err)

	for _, format := range []string{"json", "yaml"} {
		data, err := EncodeMock(mock, format)
		require.NoError(t, err)

		back, err := DecodeMock(m, data, format)
		require.NoError(t, err)

		desc, err := back.MarshalJSON()
		require.NoError(t, err)
		require.Contains(t, string(desc), `"json":{"id":9007199254740993}`, format)
		require.Contains(t, string(desc), `"maxProperties":9007199254740993`, format)
	}
}
