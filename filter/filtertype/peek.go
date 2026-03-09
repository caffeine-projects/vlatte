package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// PeekMatcher runs an action then matches with the inner matcher.
type PeekMatcher struct {
	Matcher vlatte.Filter
	Action  func(v any) error
}

// Match implements vlatte.Filter.
func (m *PeekMatcher) Match(v any) (bool, vlatte.Reason, error) {
	err := m.Action(v)
	if err != nil {
		return Error(fmt.Errorf("peek: %w", err))
	}

	return m.Matcher.Match(v)
}
