package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// peekMatcher runs an action then matches with the inner matcher.
type peekMatcher struct {
	Matcher vlatte.Matcher
	Action  func(v any) error
}

// Name implements vlatte.Matcher.
func (m *peekMatcher) Name() string { return "Peek" }

// Match implements vlatte.Matcher.
func (m *peekMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	err := m.Action(arg.Value())
	if err != nil {
		return failure(fmt.Errorf("peek: %w", err))
	}

	return m.Matcher.Match(arg)
}
