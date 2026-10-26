package handler

import (
	"cmp"
	"slices"
)

// Scenario is the state of a scenario.
type Scenario struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// ScenarioList is the answer of [Operations.ListScenarios]: GET /scenarios.
type ScenarioList struct {
	Scenarios []Scenario `json:"scenarios"`
}

// ListScenarios returns the state of every scenario, in the order of their names. A scenario shows once a mock
// started it, or its state was set. It needs a server that keeps scenarios.
func (o *Operations) ListScenarios() (ScenarioList, error) {
	if o.scenarios == nil {
		return ScenarioList{}, notSupported(_lackingScenarios)
	}

	states := o.scenarios.Scenarios()

	list := ScenarioList{Scenarios: make([]Scenario, 0, len(states))}
	for name, state := range states {
		list.Scenarios = append(list.Scenarios, Scenario{Name: name, State: state})
	}

	slices.SortFunc(list.Scenarios, func(a, b Scenario) int { return cmp.Compare(a.Name, b.Name) })

	return list, nil
}
