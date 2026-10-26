package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockDocument is the input of a tool that takes a mock.
type mockDocument struct {
	Mock jsontext.Value `json:"mock" jsonschema:"The mock, as a mock file writes it: see mock_schema."`
}

func addAddMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "add_mock",
		Description: "Add a mock, which the server serves at once. An id in use is refused: replace_mock replaces a mock.",
	}

	addTool(s, tool, func(ctx context.Context, in mockDocument) (any, error) {
		return ops.AddMock(ctx, in.Mock, _formatJSON)
	})
}
