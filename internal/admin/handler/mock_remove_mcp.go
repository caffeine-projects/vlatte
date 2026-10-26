package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addRemoveMockTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "remove_mock",
		Description: "Remove a mock.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true)},
	}

	addTool(s, tool, func(ctx context.Context, in mockID) (any, error) { return nil, ops.RemoveMock(ctx, in.ID) })
}
