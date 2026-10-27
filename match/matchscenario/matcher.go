package matchscenario

import (
	"errors"
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

// errKeyType is the error of a [Matcher] whose argument is no key.
var errKeyType = errors.New("scenario: key is not a string")

// Matcher is the matcher of a mock of a scenario, as [Store.Matcher] makes it.
type Matcher struct {
	store    *Store
	name     string
	required string
	next     string
	key      string
}

// Matcher returns the matcher of a mock of the scenario name. It matches while the scenario is in required, and in
// any state when required is empty. When the mock is picked to answer, [Matcher.Claim] moves the scenario to next, and
// leaves it where it is when next is empty.
//
// The value of the [vlatte.MatcherArg] of Match, Claim and Move is the key of the instance of the scenario the request
// plays in, as [Key] picks it: a string, or nil for the shared instance. A mock server selects it for a keyed mock,
// such as by rendering its key template for the request. An error as the value is the error of Match.
func (s *Store) Matcher(name, required, next string) *Matcher {
	return &Matcher{store: s, name: name, required: required, next: next}
}

// KeyedBy records source, what the mock's server makes the key of a request of, such as a template, and returns m.
// The matcher describes it, and doesn't read it: the key is the value of the argument of Match.
func (m *Matcher) KeyedBy(source string) *Matcher {
	m.key = source
	return m
}

// Name implements vlatte.Matcher.
func (m *Matcher) Name() string { return "Scenario" }

// Match implements vlatte.Matcher. It changes no state: [Matcher.Claim] does.
func (m *Matcher) Match(arg vlatte.MatcherArg) (bool, error) {
	k, err := instanceKey(arg.Value())
	if err != nil {
		return false, err
	}

	return m.required == "" || m.store.State(m.name, Key(k)) == m.required, nil
}

// Claim implements vlatte.Claimer. In one step, it checks the scenario's instance is still in the state the mock
// requires and moves it to the mock's new state. It reports false, moving nothing, when another request moved the
// instance since the mock matched, or the key of arg is an error. Otherwise, a mock that requires no state takes it.
func (m *Matcher) Claim(arg vlatte.MatcherArg) bool {
	k, err := instanceKey(arg.Value())

	return err == nil && m.store.claim(m.name, k, m.required, m.next)
}

// String names the scenario, the state the mock needs and the one the scenario is in, such as "scenario checkout
// needs started, is paid", for a report of a request no mock matched. A keyed mock's scenario has a state for each
// key: it says the state the mock needs only.
func (m *Matcher) String() string {
	switch {
	case m.required == "":
		return "scenario " + m.name
	case m.key != "":
		return "scenario " + m.name + " needs " + m.required
	}

	return "scenario " + m.name + " needs " + m.required + ", is " + m.store.State(m.name)
}

// Describe implements descriptor.SelfDescriptive.
func (m *Matcher) Describe() descriptor.Descriptor {
	props := map[string]any{
		"name":           m.name,
		"required_state": m.required,
		"new_state":      m.next,
	}
	if m.key != "" {
		props["key"] = m.key
	}

	return descriptor.SingleArg("scenario", props)
}

// Move is a move of a scenario a mock makes when it is picked: of the instance of Key, the shared one when it is
// empty, from the state it requires, empty when it requires none, to its new state.
type Move struct {
	Scenario string `json:"scenario"`
	Key      string `json:"key,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to"`
}

// Move returns the move the mock makes when it is picked for the request of arg, the argument its Claim got, and
// false when it has no new state, so moves nothing.
func (m *Matcher) Move(arg vlatte.MatcherArg) (Move, bool) {
	k, _ := instanceKey(arg.Value())

	return Move{Scenario: m.name, Key: k, From: m.required, To: m.next}, m.next != ""
}

// String says what the move does, such as "checkout moved from started to paid", or "checkout (key t1) moved from
// started to paid" for a keyed instance.
func (mv Move) String() string {
	name := Label(mv.Scenario, Key(mv.Key))
	if mv.From == "" {
		return name + " moved to " + mv.To
	}

	return name + " moved from " + mv.From + " to " + mv.To
}

// instanceKey returns the key v, the value of the argument of a [Matcher], names: "" for nil.
func instanceKey(v any) (string, error) {
	switch k := v.(type) {
	case nil:
		return "", nil
	case string:
		return k, nil
	case error:
		return "", fmt.Errorf("scenario: key: %w", k)
	}

	return "", fmt.Errorf("%w: %T", errKeyType, v)
}
