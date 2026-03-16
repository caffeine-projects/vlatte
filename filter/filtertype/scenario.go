package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
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
	if m.RequiredState == ScenarioStateStarted {
		scenarioRmu.Lock()
		m.Store.createNewIfNeeded(m.ScenarioName)
		scenarioRmu.Unlock()
	}

	scenarioRmu.RLock()
	scn, ok := m.Store.fetchByName(m.ScenarioName)
	scenarioRmu.RUnlock()
	if !ok {
		return true, nil
	}

	if scn.state == m.RequiredState {
		return true, nil
	}

	return Mismatch(fmt.Sprintf(
		"Scenario(%s): Required state: %s. Actual: %s",
		m.ScenarioName,
		m.RequiredState,
		scn.state,
	))
}

// SetState implements vlatte.Stateful.
func (m *Scenario) SetState() error {
	scenarioRmu.RLock()
	scn, ok := m.Store.fetchByName(m.ScenarioName)
	scenarioRmu.RUnlock()
	if !ok {
		return nil
	}

	if m.NewState != "" {
		scenarioRmu.Lock()
		scn.state = m.NewState
		scenarioRmu.Unlock()
	}

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
