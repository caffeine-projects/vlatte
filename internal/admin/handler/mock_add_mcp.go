package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockDocument is the input of a tool that takes a mock.
type mockDocument struct {
	Mock map[string]any `json:"mock" jsonschema:"The mock, as a mock file writes it: see mock_schema."`
}

func addAddMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "add_mock",
		Description: "Add a mock, which the server serves at once. An id in use is refused: replace_mock replaces a mock.",
	}

	addTool(s, tool, func(ctx context.Context, in mockDocument) (any, error) {
		doc, err := document(in.Mock)
		if err != nil {
			return nil, err
		}

		return ops.AddMock(ctx, doc, _formatJSON)
	})
}
