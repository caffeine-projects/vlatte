package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Peek runs an action then matches with the inner matcher.
type Peek struct {
	Matcher vlatte.Filter
	Action  func(v any) error
}

// Name implements vlatte.Filter.
func (m *Peek) Name() string { return "Peek" }

// Match implements vlatte.Filter.
func (m *Peek) Match(arg vlatte.FilterArg) (bool, error) {
	err := m.Action(arg.Value())
	if err != nil {
		return Error(fmt.Errorf("peek: %w", err))
	}

	return m.Matcher.Match(arg)
}
