package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func addResetScenariosTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "reset_scenarios",
		Description: "Move every scenario back to where it started.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(context.Context, noInput) (any, error) { return nil, ops.ResetScenarios() })
}

// scenarioName is the input of reset_scenario.
type scenarioName struct {
	Name string  `json:"name" jsonschema:"The name of the scenario, in any case."`
	Key  *string `json:"key,omitempty" jsonschema:"The key of the only instance to reset, empty for the shared one. Without it, every instance resets."`
}

func addResetScenarioTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name:        "reset_scenario",
		Description: "Move one scenario back to where it started: every instance of it, or the one of a key.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(_ context.Context, in scenarioName) (any, error) {
		if in.Key == nil {
			return nil, ops.ResetScenario(in.Name)
		}

		return nil, ops.ResetScenario(in.Name, matchscenario.Key(*in.Key))
	})
}
