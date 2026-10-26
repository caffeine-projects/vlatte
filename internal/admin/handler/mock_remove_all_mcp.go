package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addRemoveAllMocksTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "remove_all_mocks",
		Description: "Remove every mock, of mock files and code too. reset removes only the mocks this API added.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return nil, ops.RemoveAllMocks(ctx) })
}
