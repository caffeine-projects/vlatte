package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scenarioState is the input of set_scenario.
type scenarioState struct {
	Name  string `json:"name" jsonschema:"The name of the scenario, exactly."`
	State string `json:"state" jsonschema:"The state to move it to."`
}

func addSetScenarioTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "set_scenario",
		Description: "Move a scenario to a state, starting it when no mock did yet.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(_ context.Context, in scenarioState) (any, error) {
		return ops.moveScenario(in.Name, in.State)
	})
}
