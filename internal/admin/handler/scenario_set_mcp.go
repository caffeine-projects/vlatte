package handler

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// scenarioState is the input of set_scenario.
type scenarioState struct {
	Name  string `json:"name" jsonschema:"The name of the scenario, in any case."`
	State string `json:"state" jsonschema:"The state to move it to."`
	Key   string `json:"key,omitempty" jsonschema:"The key of the instance to move. Empty moves the shared instance."`
}

func addSetScenarioTool(s *mcp.Server, ops *Operations) {
	tool := &mcp.Tool{
		Name: "set_scenario",
		Description: "Move a scenario to a state: the mocks that require it match next. " +
			"A state none of the scenario's mocks names is refused.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}

	addTool(s, tool, func(ctx context.Context, in scenarioState) (any, error) {
		return ops.moveScenario(ctx, in.Name, in.State, matchscenario.Key(in.Key))
	})
}
