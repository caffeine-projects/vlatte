package matchscenario

import (
	"strings"
	"sync"
	"unicode"

	"github.com/caffeine-projects/vlatte"
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
	mu     sync.RWMutex
	data   map[string]*scenario
	notify func(Change)
}

// Change is a change of the states of a [Store]'s scenarios, which it gives the function set with [Store.Notify].
type Change struct {
	// Scenario names the scenario that changed, or is empty when every scenario did, as ResetScenarios does.
	Scenario string

	// Message says what changed, such as "checkout moved from started to paid".
	Message string

	// Claim is the argument of the [Matcher.Claim] of a mock that moved the scenario: its Mock and Context tell which
	// mock and which request did. It is nil when no request did, as for SetScenario.
	Claim *vlatte.MatcherArg
}

// Notify sets fn as the function s gives each change, as it makes it: fn gets them in the order the scenarios changed.
// s holds its lock while fn runs, so fn must not block, nor call s.
func (s *Store) Notify(fn func(Change)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.notify = fn
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
	s.changed(Change{Scenario: name, Message: name + " set to " + state})
}

// ResetScenario moves the scenario name back to [StateStarted]. It changes nothing, and gives no [Change], when the
// store doesn't hold name: no mock moved it and SetScenario never set it, so it is in StateStarted already.
func (s *Store) ResetScenario(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if scn, ok := s.data[key(name)]; ok {
		scn.state = StateStarted
		s.changed(Change{Scenario: name, Message: name + " reset to " + StateStarted})
	}
}

// ResetScenarios moves every scenario back to [StateStarted].
func (s *Store) ResetScenarios() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scn := range s.data {
		scn.state = StateStarted
	}

	s.changed(Change{Message: "every scenario reset to " + StateStarted})
}

// View returns a view of s for a request whose mock made moves. A scenario a move of moves names is in the state that
// move took it to, even once another request moved it on, as the request's journal entry says; any other scenario is
// in the state it is in when read. A zero View reads no store, and panics.
func (s *Store) View(moves ...Move) View {
	return View{store: s, moves: moves}
}

// claim moves the scenario name from required to next, as [Matcher.Claim] does with arg.
func (s *Store) claim(name, required, next string, arg *vlatte.MatcherArg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if required != "" && s.state(name) != required {
		return false
	}

	if next != "" {
		s.scenario(name).state = next
		s.changed(Change{Scenario: name, Message: Move{Scenario: name, From: required, To: next}.String(), Claim: arg})
	}

	return true
}

// changed gives c to the function set with Notify, if any. The caller holds the write lock.
func (s *Store) changed(c Change) {
	if s.notify != nil {
		s.notify(c)
	}
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
	moves []Move
}

// State returns the state of the scenario name, whatever its case: the state the view's move of it took it to, or else
// the one [Store.State] returns.
func (v View) State(name string) string {
	k := key(name)
	for _, mv := range v.moves {
		if key(mv.Scenario) == k {
			return mv.To
		}
	}

	return v.store.State(name)
}
