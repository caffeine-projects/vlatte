package matchscenario

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Matcher             = (*Matcher)(nil)
	_ vlatte.Stateful            = (*Matcher)(nil)
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
// any state when required is empty. Once the mock answered, [Matcher.SetState] moves the scenario to next, and leaves
// it where it is when next is empty.
func (s *Store) Matcher(name, required, next string) *Matcher {
	return &Matcher{store: s, name: name, required: required, next: next}
}

// Name implements vlatte.Matcher.
func (m *Matcher) Name() string { return "Scenario" }

// Match implements vlatte.Matcher. A mock that requires [StateStarted] starts its scenario. One that requires another
// state matches while no mock started its scenario.
func (m *Matcher) Match(_ vlatte.MatcherArg) (bool, error) {
	if m.required == "" {
		return true, nil
	}

	state, ok := m.store.state(m.name, m.required == StateStarted)

	return !ok || state == m.required, nil
}

// SetState implements vlatte.Stateful. It moves the scenario to the mock's new state, starting it when no mock did
// yet.
func (m *Matcher) SetState() error {
	if m.next == "" {
		return nil
	}

	m.store.SetScenario(m.name, m.next)

	return nil
}

// Describe implements descriptor.SelfDescriptive.
func (m *Matcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("scenario", map[string]any{
		"name":           m.name,
		"required_state": m.required,
		"new_state":      m.next,
	})
}
