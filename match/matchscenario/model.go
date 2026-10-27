package matchscenario

import (
	"cmp"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
)

// ErrUnknownState is the error of [Store.Move] to a state none of the scenario's mocks names.
var ErrUnknownState = errors.New("scenario: unknown state")

// Use is a mock of a scenario: the ID of the mock, and its matcher. A mock server lists the uses of its mocks for
// [Store.Model] and [Store.Move].
type Use struct {
	MockID  string
	Matcher *Matcher
}

// Scenario is a scenario as its mocks describe it.
type Scenario struct {
	// Name is the name the first mock of the scenario gives it, or the name it was first set under when no mock
	// names it.
	Name string

	// State is the state the scenario is in.
	State string

	// States are the states the mocks of the scenario name, [StateStarted] first and the others by name. A scenario
	// no mock names, but that was set, has none.
	States []State

	// Transitions are the moves the mocks of the scenario make, in the order of the mocks.
	Transitions []Transition
}

// State is a state of a scenario.
type State struct {
	Name string

	// Reachable is false for a state a mock requires but no move of a mock leads to from [StateStarted]: only setting
	// the scenario gets there.
	Reachable bool
}

// Transition is the move a mock makes when it is picked to answer.
type Transition struct {
	// From is the state the mock requires, or "" when it matches in any.
	From string

	// To is the state the scenario moves to.
	To string

	MockID string
}

// Model returns every scenario, in the order of their names, whatever their case: those uses name, under the name of the first use, and those that moved
// without a mock.
func (s *Store) Model(uses iter.Seq[Use]) []Scenario {
	byKey := make(map[string]*Scenario)

	scenarioOf := func(name string) *Scenario {
		k := key(name)
		if scn, ok := byKey[k]; ok {
			return scn
		}

		scn := &Scenario{Name: name}
		byKey[k] = scn

		return scn
	}

	states := make(map[*Scenario]map[string]bool)
	for u := range uses {
		m := u.Matcher
		scn := scenarioOf(m.name)

		if states[scn] == nil {
			states[scn] = map[string]bool{StateStarted: true}
		}

		for _, state := range []string{m.required, m.next} {
			if state != "" {
				states[scn][state] = true
			}
		}

		if m.next != "" {
			scn.Transitions = append(scn.Transitions, Transition{From: m.required, To: m.next, MockID: u.MockID})
		}
	}

	s.mu.RLock()
	for k, moved := range s.data {
		scn, ok := byKey[k]
		if !ok {
			scn = &Scenario{Name: moved.name}
			byKey[k] = scn
		}

		scn.State = moved.state
	}
	s.mu.RUnlock()

	out := make([]Scenario, 0, len(byKey))
	for _, scn := range byKey {
		scn.State = cmp.Or(scn.State, StateStarted)
		scn.States = statesOf(states[scn], scn.Transitions)
		out = append(out, *scn)
	}

	slices.SortFunc(out, func(a, b Scenario) int {
		return cmp.Compare(strings.ToLower(key(a.Name)), strings.ToLower(key(b.Name)))
	})

	return out
}

// Move moves the scenario name to state, as [Store.SetScenario] does, once it checked a mock of uses names state: the
// scenario takes the name of the first use, whatever the case of name. It returns an error that wraps
// [ErrUnknownState] when the scenario's mocks name other states only, or state is empty. A scenario no mock names
// moves to any state but the empty one, so a test can set it before its mocks load.
func (s *Store) Move(uses iter.Seq[Use], name, state string) error {
	if state == "" {
		return fmt.Errorf("%w: %s has no state \"\"", ErrUnknownState, name)
	}

	named := make([]string, 0)
	for u := range uses {
		if key(u.Matcher.name) != key(name) {
			continue
		}

		if len(named) == 0 {
			name = u.Matcher.name
		}

		named = append(named, StateStarted, u.Matcher.required, u.Matcher.next)
	}

	if len(named) > 0 && !slices.Contains(named, state) {
		slices.Sort(named)
		named = slices.DeleteFunc(slices.Compact(named), func(s string) bool { return s == "" })

		return fmt.Errorf("%w: %s has no state %q; its states are %s", ErrUnknownState, name, state,
			strings.Join(named, ", "))
	}

	s.SetScenario(name, state)

	return nil
}

// statesOf returns the states of named, [StateStarted] first, each reachable when a path of transitions leads to it
// from [StateStarted].
func statesOf(named map[string]bool, transitions []Transition) []State {
	if len(named) == 0 {
		return nil
	}

	reachable := map[string]bool{StateStarted: true}
	for grew := true; grew; {
		grew = false

		for _, t := range transitions {
			if !reachable[t.To] && (t.From == "" || reachable[t.From]) {
				reachable[t.To], grew = true, true
			}
		}
	}

	out := make([]State, 0, len(named))
	for name := range named {
		out = append(out, State{Name: name, Reachable: reachable[name]})
	}

	slices.SortFunc(out, func(a, b State) int {
		switch {
		case a.Name == StateStarted:
			return -1
		case b.Name == StateStarted:
			return 1
		}

		return cmp.Compare(a.Name, b.Name)
	})

	return out
}
