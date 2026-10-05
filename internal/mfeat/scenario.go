package mfeat

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// Scenario returns a matcher that matches based on scenario state.
func Scenario(store *ScenarioStore, name, requiredState, newState string) *scenarioMatcher {
	return &scenarioMatcher{
		Store:         store,
		RequiredState: requiredState,
		NewState:      newState,
		ScenarioName:  name,
	}
}

// scenarioMatcher matches based on scenario state (stateful).
type scenarioMatcher struct {
	Store         *ScenarioStore
	RequiredState string
	NewState      string
	ScenarioName  string
}

// Name implements vlatte.Matcher.
func (m *scenarioMatcher) Name() string { return "Scenario" }

// Match implements vlatte.Matcher.
func (m *scenarioMatcher) Match(_ vlatte.MatcherArg) (bool, error) {
	store := m.Store

	if m.RequiredState == ScenarioStateStarted {
		store.mu.Lock()
		store.createNewIfNeeded(m.ScenarioName)
		store.mu.Unlock()
	}

	store.mu.RLock()
	scn, ok := store.fetchByName(m.ScenarioName)
	var currentState string
	if ok {
		currentState = scn.state
	}
	store.mu.RUnlock()

	if !ok {
		return true, nil
	}

	return currentState == m.RequiredState, nil
}

// SetState implements vlatte.Stateful.
func (m *scenarioMatcher) SetState() error {
	if m.NewState == "" {
		return nil
	}

	store := m.Store
	store.mu.Lock()
	defer store.mu.Unlock()

	scn, ok := store.fetchByName(m.ScenarioName)
	if !ok {
		return nil
	}

	scn.state = m.NewState
	return nil
}

// Describe implements descriptor.SelfDescriptive.
func (m *scenarioMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("scenario", map[string]any{
		"name":           m.ScenarioName,
		"required_state": m.RequiredState,
		"new_state":      m.NewState,
	})
}
