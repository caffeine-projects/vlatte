package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addListScenariosTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "list_scenarios",
		Description: "List every scenario, mocks that match in turn as a scenario moves from state to state: " +
			"its state, the states its mocks name, and which mock moves it where.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(ctx context.Context, _ noInput) (any, error) { return ops.ListScenarios(ctx) })
}
