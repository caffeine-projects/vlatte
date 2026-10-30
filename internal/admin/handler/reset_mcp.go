package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addResetTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "reset",
		Description: "Remove the mocks this API added, load the mock files again, start every scenario over, and clear " +
			"the journal of requests. Mocks added in code, and the key-value store, stay.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return nil, ops.Reset(ctx) })
}
