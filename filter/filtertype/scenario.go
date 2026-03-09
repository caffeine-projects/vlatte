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
	Name          string
}

// Match implements vlatte.Filter.
func (m *Scenario) Match(_ any) (bool, vlatte.Reason, error) {
	if m.RequiredState == ScenarioStateStarted {
		scenarioRmu.Lock()
		m.Store.createNewIfNeeded(m.Name)
		scenarioRmu.Unlock()
	}

	scenarioRmu.RLock()
	scn, ok := m.Store.fetchByName(m.Name)
	scenarioRmu.RUnlock()
	if !ok {
		return true, vlatte.Reason(""), nil
	}

	if scn.state == m.RequiredState {
		return true, vlatte.Reason(""), nil
	}

	return false, vlatte.Reason(fmt.Sprintf(
		"Scenario(%s): Required state: %s. Actual: %s",
		scn.name,
		m.RequiredState,
		scn.state,
	)), nil
}

// SetState implements vlatte.Stateful.
func (m *Scenario) SetState() error {
	scenarioRmu.RLock()
	scn, ok := m.Store.fetchByName(m.Name)
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
		"name":           m.Name,
		"required_state": m.RequiredState,
		"new_state":      m.NewState,
	})
}
