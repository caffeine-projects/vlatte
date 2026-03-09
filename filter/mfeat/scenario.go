package mfeat

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Re-export for backward compatibility.
const ScenarioStateStarted = filtertype.ScenarioStateStarted

// ScenarioStore is an alias for filtertype.ScenarioStore.
type ScenarioStore = filtertype.ScenarioStore

// NewScenarioStore creates a new scenario store.
func NewScenarioStore() *ScenarioStore {
	return filtertype.NewScenarioStore()
}

// Scenario returns a filter that matches based on scenario state.
func Scenario(store *ScenarioStore, name, requiredState, newState string) *filtertype.Scenario {
	return &filtertype.Scenario{
		Store:         store,
		RequiredState: requiredState,
		NewState:      newState,
		Name:          name,
	}
}
