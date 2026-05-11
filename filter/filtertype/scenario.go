package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// Scenario matches based on scenario state (stateful).
type Scenario struct {
	Store         *ScenarioStore
	RequiredState string
	NewState      string
	ScenarioName  string
}

// Name implements vlatte.Filter.
func (m *Scenario) Name() string { return "Scenario" }

// Match implements vlatte.Filter.
func (m *Scenario) Match(_ vlatte.FilterArg) (bool, error) {
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

	if currentState == m.RequiredState {
		return true, nil
	}

	return Mismatch(fmt.Sprintf(
		"Scenario(%s): Required state: %s. Actual: %s",
		m.ScenarioName,
		m.RequiredState,
		currentState,
	))
}

// SetState implements vlatte.Stateful.
func (m *Scenario) SetState() error {
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
func (m *Scenario) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("scenario", map[string]any{
		"name":           m.ScenarioName,
		"required_state": m.RequiredState,
		"new_state":      m.NewState,
	})
}
