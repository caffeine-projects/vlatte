package handler

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mockState is the input of update_mock.
type mockState struct {
	ID      string `json:"id" jsonschema:"The id of the mock."`
	Enabled bool   `json:"enabled" jsonschema:"Whether the mock matches requests."`
}

func addUpdateMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "update_mock",
		Description: "Enable or disable a mock: a disabled mock matches no request, and keeps its hits.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, in mockState) (any, error) {
		return ops.UpdateMock(ctx, in.ID, fmt.Appendf(nil, `{"enabled": %t}`, in.Enabled), _formatJSON)
	})
}
