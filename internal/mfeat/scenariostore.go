package mfeat

import (
	"sync"
)

const ScenarioStateStarted = "started"

type scenarioState struct {
	name  string
	state string
}

func newScenarioState(name string) *scenarioState {
	return &scenarioState{name: name, state: ScenarioStateStarted}
}

// ScenarioStore holds scenario state by name. Names are exact: Checkout and checkout are two scenarios.
type ScenarioStore struct {
	mu   sync.RWMutex
	data map[string]*scenarioState
}

// NewScenarioStore creates a new scenario store.
func NewScenarioStore() *ScenarioStore {
	return &ScenarioStore{data: make(map[string]*scenarioState)}
}

// Scenarios returns the state of every scenario, by name: those a mock started, and those SetScenario set.
func (store *ScenarioStore) Scenarios() map[string]string {
	store.mu.RLock()
	defer store.mu.RUnlock()

	states := make(map[string]string, len(store.data))
	for name, s := range store.data {
		states[name] = s.state
	}

	return states
}

// SetScenario moves the scenario name to state, starting it when no mock did yet.
func (store *ScenarioStore) SetScenario(name, state string) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.createNewIfNeeded(name).state = state
}

// ResetScenarios moves every scenario back to where it started. Scenarios are kept, not removed: a mock of a scenario
// no mock started matches whatever the state it requires.
func (store *ScenarioStore) ResetScenarios() {
	store.mu.Lock()
	defer store.mu.Unlock()

	for _, s := range store.data {
		s.state = ScenarioStateStarted
	}
}

func (store *ScenarioStore) fetchByName(name string) (*scenarioState, bool) {
	s, ok := store.data[name]
	return s, ok
}

func (store *ScenarioStore) createNewIfNeeded(name string) *scenarioState {
	s, ok := store.fetchByName(name)
	if !ok {
		s = newScenarioState(name)
		store.data[name] = s
	}

	return s
}
