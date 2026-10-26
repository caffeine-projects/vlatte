package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addResetScenariosTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "reset_scenarios",
		Description: "Move every scenario back to where it started.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return nil, ops.ResetScenarios() })
}
