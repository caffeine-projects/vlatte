package matchscenario

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Matcher             = (*Matcher)(nil)
	_ vlatte.Claimer             = (*Matcher)(nil)
	_ fmt.Stringer               = (*Matcher)(nil)
	_ descriptor.SelfDescriptive = (*Matcher)(nil)
)

// Matcher is the matcher of a mock of a scenario, as [Store.Matcher] makes it.
type Matcher struct {
	store    *Store
	name     string
	required string
	next     string
}

// Matcher returns the matcher of a mock of the scenario name. It matches while the scenario is in required, and in
// any state when required is empty. When the mock is picked to answer, [Matcher.Claim] moves the scenario to next, and
// leaves it where it is when next is empty.
func (s *Store) Matcher(name, required, next string) *Matcher {
	return &Matcher{store: s, name: name, required: required, next: next}
}

// Name implements vlatte.Matcher.
func (m *Matcher) Name() string { return "Scenario" }

// Match implements vlatte.Matcher. It changes no state: [Matcher.Claim] does.
func (m *Matcher) Match(_ vlatte.MatcherArg) (bool, error) {
	return m.required == "" || m.store.State(m.name) == m.required, nil
}

// Claim implements vlatte.Claimer. In one step, it checks the scenario is still in the state the mock requires and
// moves it to the mock's new state. It reports false, moving nothing, when another request moved the scenario since
// the mock matched. A mock that requires no state always takes it. The [Change] of a move has arg.
func (m *Matcher) Claim(arg vlatte.MatcherArg) bool {
	return m.store.claim(m.name, m.required, m.next, &arg)
}

// String names the scenario, the state the mock needs and the one the scenario is in, such as "scenario checkout
// needs started, is paid", for a report of a request no mock matched.
func (m *Matcher) String() string {
	if m.required == "" {
		return "scenario " + m.name
	}

	return "scenario " + m.name + " needs " + m.required + ", is " + m.store.State(m.name)
}

// Describe implements descriptor.SelfDescriptive.
func (m *Matcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("scenario", map[string]any{
		"name":           m.name,
		"required_state": m.required,
		"new_state":      m.next,
	})
}

// Move is a move of a scenario a mock makes when it is picked: from the state it requires, empty when it requires none,
// to its new state.
type Move struct {
	Scenario string `json:"scenario"`
	From     string `json:"from,omitempty"`
	To       string `json:"to"`
}

// Move returns the move the mock makes when it is picked, and false when it has no new state, so moves nothing.
func (m *Matcher) Move() (Move, bool) {
	return Move{Scenario: m.name, From: m.required, To: m.next}, m.next != ""
}

// String says what the move does, such as "checkout moved from started to paid".
func (mv Move) String() string {
	if mv.From == "" {
		return mv.Scenario + " moved to " + mv.To
	}

	return mv.Scenario + " moved from " + mv.From + " to " + mv.To
}
