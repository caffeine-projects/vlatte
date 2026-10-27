package matchscenario

import (
	"strings"
	"sync"
	"unicode"
)

// StateStarted is the state a scenario starts in, and the one a reset moves it back to.
const StateStarted = "started"

// scenario is the state of one scenario, under the name it was first given.
type scenario struct {
	name  string
	state string
}

// Store holds the state of scenarios by name. Names ignore case, as [strings.EqualFold] does: Checkout and checkout
// are one scenario, listed under the name it was first given. It is safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	data map[string]*scenario
}

// New creates an empty store.
func New() *Store {
	return &Store{data: make(map[string]*scenario)}
}

// Scenarios returns the state of every scenario, by name: those a mock started, and those SetScenario set.
func (s *Store) Scenarios() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := make(map[string]string, len(s.data))
	for _, scn := range s.data {
		states[scn.name] = scn.state
	}

	return states
}

// SetScenario moves the scenario name to state, starting it when no mock did yet.
func (s *Store) SetScenario(name, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.start(name).state = state
}

// ResetScenarios moves every scenario back to [StateStarted]. Scenarios are kept, not removed: a mock of a scenario
// no mock started matches whatever the state it requires.
func (s *Store) ResetScenarios() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scn := range s.data {
		scn.state = StateStarted
	}
}

// claim moves the scenario name from required to next, as [Matcher.Claim] does.
func (s *Store) claim(name, required, next string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if required == StateStarted {
		s.start(name)
	}

	if scn, ok := s.data[key(name)]; ok && required != "" && scn.state != required {
		return false
	}

	if next != "" {
		s.start(name).state = next
	}

	return true
}

// state returns the state of the scenario name, and whether it started. With start, it starts the scenario first.
func (s *Store) state(name string, start bool) (string, bool) {
	if start {
		s.mu.Lock()
		defer s.mu.Unlock()

		return s.start(name).state, true
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	scn, ok := s.data[key(name)]
	if !ok {
		return "", false
	}

	return scn.state, true
}

// start returns the scenario name, starting it when it hasn't. The caller holds the write lock.
func (s *Store) start(name string) *scenario {
	k := key(name)

	scn, ok := s.data[k]
	if !ok {
		scn = &scenario{name: name, state: StateStarted}
		s.data[k] = scn
	}

	return scn
}

// key is the key of the scenario name in the store: names that strings.EqualFold finds equal have the same key.
func key(name string) string {
	return strings.Map(func(r rune) rune {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			least = min(least, f)
		}

		return least
	}, name)
}
