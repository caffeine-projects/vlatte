package matchscenario

import (
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/caffeine-projects/vlatte"
)

// StateStarted is the state every scenario starts in, and the one a reset moves it back to.
const StateStarted = "started"

// scenario is the state of one scenario, under the name it was first given.
type scenario struct {
	name string

	// state is the state of the shared instance, and shared whether it moved.
	state  string
	shared bool

	// keys are the states of the keyed instances that moved, by key.
	keys map[string]string
}

// Instance picks the instance of a scenario a call acts on, as [Key] makes it.
type Instance struct {
	key string
}

// Key picks the instance of a scenario keyed key: the one requests play in whose mocks' key is key. Each key is a
// scenario of its own, starting in [StateStarted], so tests that run at once don't share one. Keys are exact: t1 and
// T1 are two instances. Key("") is the shared instance, the one of mocks without a key.
func Key(key string) Instance {
	return Instance{key: key}
}

// keyOf returns the key in picks, the last one, and whether it picks any.
func keyOf(in []Instance) (string, bool) {
	if len(in) == 0 {
		return "", false
	}

	return in[len(in)-1].key, true
}

// Label names the instance of the scenario name in picks, such as "checkout" for the shared one and "checkout (key
// t1)" for a keyed one, for a message that says what moved.
func Label(name string, in ...Instance) string {
	if k, _ := keyOf(in); k != "" {
		return name + " (key " + k + ")"
	}

	return name
}

// Store holds the state of scenarios by name. Every scenario is in [StateStarted] until a mock or SetScenario moves
// it. Names ignore case, as [strings.EqualFold] does: Checkout and checkout are one scenario, listed under the name it
// was first given. A scenario has a shared instance, and one for each key its keyed mocks name: see [Key]. It is safe
// for concurrent use.
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

// Scenarios returns the state of the shared instance of every scenario that moved, by name: those a mock moved, and
// those SetScenario set. A scenario that never moved is in [StateStarted], and isn't listed.
func (s *Store) Scenarios() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := make(map[string]string, len(s.data))
	for _, scn := range s.data {
		if scn.shared {
			states[scn.name] = scn.state
		}
	}

	return states
}

// State returns the state of the scenario name: of its shared instance, or of the one in picks.
func (s *Store) State(name string, in ...Instance) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	k, _ := keyOf(in)

	return s.state(name, k)
}

// SetScenario moves the scenario name to state, whatever states its mocks name: its shared instance, or the one in
// picks. [Store.Move] checks the state first.
func (s *Store) SetScenario(name, state string, in ...Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k, _ := keyOf(in)
	s.set(name, k, state)
	s.changed(Change{Scenario: name, Message: Label(name, in...) + " set to " + state})
}

// ResetScenario moves the scenario name back to [StateStarted]: every instance of it, or only the one in picks. It
// changes nothing, and gives no [Change], when the store doesn't hold that instance: nothing moved it and SetScenario
// never set it, so it is in StateStarted already.
func (s *Store) ResetScenario(name string, in ...Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()

	scn, ok := s.data[key(name)]
	if !ok {
		return
	}

	k, picked := keyOf(in)
	switch {
	case !picked:
		scn.state = StateStarted
		clear(scn.keys)
	case k == "":
		scn.state = StateStarted
	default:
		if _, ok := scn.keys[k]; !ok {
			return
		}

		delete(scn.keys, k)
	}

	s.changed(Change{Scenario: name, Message: Label(name, in...) + " reset to " + StateStarted})
}

// ResetScenarios moves every instance of every scenario back to [StateStarted].
func (s *Store) ResetScenarios() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scn := range s.data {
		scn.state = StateStarted
		clear(scn.keys)
	}

	s.changed(Change{Message: "every scenario reset to " + StateStarted})
}

// View returns a view of s for a request whose mock made moves. A scenario a move of moves names is in the state that
// move took it to, even once another request moved it on, as the request's journal entry says; any other scenario is
// in the state it is in when read. A zero View reads no store, and panics.
func (s *Store) View(moves ...Move) View {
	return View{store: s, moves: moves}
}

// claim moves the instance k of the scenario name from required to next, as [Matcher.Claim] does with arg.
func (s *Store) claim(name, k, required, next string, arg *vlatte.MatcherArg) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if required != "" && s.state(name, k) != required {
		return false
	}

	if next != "" {
		s.set(name, k, next)
		s.changed(Change{
			Scenario: name,
			Message:  Move{Scenario: name, Key: k, From: required, To: next}.String(),
			Claim:    arg,
		})
	}

	return true
}

// changed gives c to the function set with Notify, if any. The caller holds the write lock.
func (s *Store) changed(c Change) {
	if s.notify != nil {
		s.notify(c)
	}
}

// state returns the state of the instance k of the scenario name. The caller holds the lock.
func (s *Store) state(name, k string) string {
	scn, ok := s.data[key(name)]
	switch {
	case !ok:
		return StateStarted
	case k == "":
		return scn.state
	}

	if state, ok := scn.keys[k]; ok {
		return state
	}

	return StateStarted
}

// set moves the instance k of the scenario name to state, adding the scenario when the store has none. The caller
// holds the write lock.
func (s *Store) set(name, k, state string) {
	nk := key(name)

	scn, ok := s.data[nk]
	if !ok {
		scn = &scenario{name: name, state: StateStarted}
		s.data[nk] = scn
	}

	if k == "" {
		scn.state, scn.shared = state, true
		return
	}

	if scn.keys == nil {
		scn.keys = make(map[string]string)
	}

	scn.keys[k] = state
}

// instances returns the keyed instances of scn that moved, in the order of their keys, or nil when none did. The
// caller holds the lock.
func (scn *scenario) instances() []KeyedInstance {
	if len(scn.keys) == 0 {
		return nil
	}

	out := make([]KeyedInstance, 0, len(scn.keys))
	for _, k := range slices.Sorted(maps.Keys(scn.keys)) {
		out = append(out, KeyedInstance{Key: k, State: scn.keys[k]})
	}

	return out
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

// State returns the state of the shared instance of the scenario name, whatever its case: the state the view's move of
// it took it to, or else the one [Store.State] returns.
func (v View) State(name string) string {
	return v.StateOf(name, "")
}

// StateOf returns the state of the instance of the scenario name keyed key, the shared one when key is empty: the state
// the view's move of it took it to, or else the one [Store.State] returns with [Key].
func (v View) StateOf(name, k string) string {
	for _, mv := range v.moves {
		if mv.Key == k && key(mv.Scenario) == key(name) {
			return mv.To
		}
	}

	return v.store.State(name, Key(k))
}
