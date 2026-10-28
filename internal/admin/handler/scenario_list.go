package handler

import (
	"context"
	"fmt"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// Scenario is a scenario: where its shared instance and its keyed ones are, the states its mocks name, and the moves
// they make.
type Scenario struct {
	Name        string               `json:"name"`
	State       string               `json:"state"`
	Instances   []ScenarioInstance   `json:"instances"`
	States      []ScenarioState      `json:"states"`
	Transitions []ScenarioTransition `json:"transitions"`
}

// ScenarioInstance is the instance of a scenario of one key, which the mocks with a scenario key move for each key of
// their requests.
type ScenarioInstance struct {
	Key   string `json:"key"`
	State string `json:"state"`
}

// ScenarioState is a state a mock of a scenario names. An unreachable one is a state no path of mock moves leads to
// from started: requests get there only after the scenario is set to it or to another such state.
type ScenarioState struct {
	Name      string `json:"name"`
	Reachable bool   `json:"reachable"`
}

// ScenarioTransition is the move a mock makes when it is picked to answer: from a state, or any when From is empty,
// to another.
type ScenarioTransition struct {
	From   string `json:"from,omitempty"`
	To     string `json:"to"`
	MockID string `json:"mock_id"`
}

// ScenarioList is the answer of [Operations.ListScenarios]: GET /scenarios.
type ScenarioList struct {
	Scenarios []Scenario `json:"scenarios"`
}

// ListScenarios returns every scenario, in the order of their names: those the server's mocks name, and those set
// without a mock. It needs a server that keeps scenarios.
func (o *Operations) ListScenarios(ctx context.Context) (ScenarioList, error) {
	if o.scenarios == nil {
		return ScenarioList{}, notSupported(_lackingScenarios)
	}

	model, err := o.scenarios.Scenarios(ctx)
	if err != nil {
		return ScenarioList{}, fmt.Errorf("admin: listing scenarios: %w", err)
	}

	list := ScenarioList{Scenarios: make([]Scenario, 0, len(model))}
	for _, scn := range model {
		list.Scenarios = append(list.Scenarios, scenarioOf(scn))
	}

	return list, nil
}

// scenarioOf is the answer of scn.
func scenarioOf(scn matchscenario.Scenario) Scenario {
	out := Scenario{
		Name:        scn.Name,
		State:       scn.State,
		Instances:   make([]ScenarioInstance, 0, len(scn.Instances)),
		States:      make([]ScenarioState, 0, len(scn.States)),
		Transitions: make([]ScenarioTransition, 0, len(scn.Transitions)),
	}

	for _, in := range scn.Instances {
		out.Instances = append(out.Instances, ScenarioInstance{Key: in.Key, State: in.State})
	}

	for _, s := range scn.States {
		out.States = append(out.States, ScenarioState{Name: s.Name, Reachable: s.Reachable})
	}

	for _, t := range scn.Transitions {
		out.Transitions = append(out.Transitions, ScenarioTransition{From: t.From, To: t.To, MockID: t.MockID})
	}

	return out
}
