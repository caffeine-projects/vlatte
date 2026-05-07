package filtertype

import (
	"strings"
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

// ScenarioStore holds scenario state by name.
type ScenarioStore struct {
	mu   sync.RWMutex
	data map[string]*scenarioState
}

// NewScenarioStore creates a new scenario store.
func NewScenarioStore() *ScenarioStore {
	return &ScenarioStore{data: make(map[string]*scenarioState)}
}

func (store *ScenarioStore) fetchByName(name string) (*scenarioState, bool) {
	s, ok := store.data[strings.ToLower(strings.TrimSpace(name))]
	return s, ok
}

func (store *ScenarioStore) createNewIfNeeded(name string) *scenarioState {
	s, ok := store.fetchByName(name)
	if !ok {
		scenario := newScenarioState(name)
		store.data[scenario.name] = scenario
		return scenario
	}
	return s
}
