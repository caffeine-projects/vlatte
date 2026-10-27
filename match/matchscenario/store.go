package matchscenario

import (
	"strings"
	"sync"
	"unicode"
)

// StateStarted is the state every scenario starts in, and the one a reset moves it back to.
const StateStarted = "started"

// scenario is the state of one scenario, under the name it was first given.
type scenario struct {
	name  string
	state string
}

// Store holds the state of scenarios by name. Every scenario is in [StateStarted] until a mock or SetScenario moves
// it. Names ignore case, as [strings.EqualFold] does: Checkout and checkout are one scenario, listed under the name it
// was first given. It is safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	data map[string]*scenario
}

// New creates an empty store.
func New() *Store {
	return &Store{data: make(map[string]*scenario)}
}

// Scenarios returns the state of every scenario that moved, by name: those a mock moved, and those SetScenario set.
// A scenario that never moved is in [StateStarted], and isn't listed.
func (s *Store) Scenarios() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := make(map[string]string, len(s.data))
	for _, scn := range s.data {
		states[scn.name] = scn.state
	}

	return states
}

// State returns the state of the scenario name.
func (s *Store) State(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if scn, ok := s.data[key(name)]; ok {
		return scn.state
	}

	return StateStarted
}

// SetScenario moves the scenario name to state, whatever states its mocks name. [Store.Move] checks the state first.
func (s *Store) SetScenario(name, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.scenario(name).state = state
}

// ResetScenario moves the scenario name back to [StateStarted].
func (s *Store) ResetScenario(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if scn, ok := s.data[key(name)]; ok {
		scn.state = StateStarted
	}
}

// ResetScenarios moves every scenario back to [StateStarted].
func (s *Store) ResetScenarios() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scn := range s.data {
		scn.state = StateStarted
	}
}

// View returns a view of s that sees every later move. A zero View reads no store, and panics.
func (s *Store) View() View {
	return View{store: s}
}

// claim moves the scenario name from required to next, as [Matcher.Claim] does.
func (s *Store) claim(name, required, next string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if required != "" && s.state(name) != required {
		return false
	}

	if next != "" {
		s.scenario(name).state = next
	}

	return true
}

// state returns the state of the scenario name. The caller holds the lock.
func (s *Store) state(name string) string {
	if scn, ok := s.data[key(name)]; ok {
		return scn.state
	}

	return StateStarted
}

// scenario returns the scenario name, adding it in [StateStarted] when the store has none. The caller holds the write
// lock.
func (s *Store) scenario(name string) *scenario {
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

// View reads the states of the scenarios of a [Store], and changes none: what templates get.
type View struct {
	store *Store
}

// State returns the state of the scenario name, whatever its case, as [Store.State] does.
func (v View) State(name string) string {
	return v.store.State(name)
}
