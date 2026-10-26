package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addReloadMocksTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "reload_mocks",
		Description: "Load the mock files, and the persistent store of the mocks this API adds, again. A mock file's mock " +
			"takes the place of a mock this API added with its id.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return nil, ops.ReloadMocks() })
}
