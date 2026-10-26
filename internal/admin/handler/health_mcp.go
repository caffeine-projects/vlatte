package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addHealthTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "health",
		Description: "Check the mock server: its kind, such as http, and the URL it serves its mocks at.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return ops.Health(), nil })
}
