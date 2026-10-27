package handler

import (
	"context"
	"encoding/json/jsontext"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockReplacement is the input of replace_mock.
type mockReplacement struct {
	ID   string         `json:"id" jsonschema:"The id of the mock to replace."`
	Mock jsontext.Value `json:"mock" jsonschema:"The new mock, as a mock file writes it, with the same id: get_mock gives it."`
}

func addReplaceMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "replace_mock",
		Description: "Replace a mock, in one step: a request finds the old mock or the new one, never neither.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, in mockReplacement) (any, error) {
		return ops.ReplaceMock(ctx, in.ID, in.Mock, _formatJSON)
	})
}
