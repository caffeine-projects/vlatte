package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addMockSchemaTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "mock_schema",
		Description: "Get the JSON Schema of the server's mock files, which add_mock, validate_mock and replace_mock take.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) {
		schema, err := ops.MockSchema()
		return jsontext.Value(schema), err
	})
}
