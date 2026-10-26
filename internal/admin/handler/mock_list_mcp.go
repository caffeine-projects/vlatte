package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addListMocksTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "list_mocks",
		Description: "List every mock, enabled or not, in the order mocks match requests, with its priority, hits and origin.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return ops.ListMocks(ctx) })
}
