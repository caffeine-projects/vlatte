package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addListScenariosTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "list_scenarios",
		Description: "List the state of every scenario: mocks that match in turn, as a scenario moves from state to state.",
		Annotations: _readOnly,
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return ops.ListScenarios() })
}
