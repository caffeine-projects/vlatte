package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addClearRequestsTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "clear_requests",
		Description: "Remove every request of the journal.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: new(true), IdempotentHint: true},
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return nil, ops.ClearRequests() })
}
