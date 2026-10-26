package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockReplacement is the input of replace_mock.
type mockReplacement struct {
	ID   string         `json:"id" jsonschema:"The id of the mock to replace."`
	Mock map[string]any `json:"mock" jsonschema:"The new mock, as a mock file writes it, with the same id or none."`
}

func addReplaceMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "replace_mock",
		Description: "Replace a mock, in one step: a request finds the old mock or the new one, never neither.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, in mockReplacement) (any, error) {
		if _, ok := in.Mock["id"]; !ok {
			in.Mock["id"] = in.ID
		}

		doc, err := document(in.Mock)
		if err != nil {
			return nil, err
		}

		return ops.ReplaceMock(ctx, in.ID, doc, _formatJSON)
	})
}
