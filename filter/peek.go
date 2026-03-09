package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

type peekMatcher struct {
	matcher vlatte.Filter
	action  func(v any) error
}

func (m *peekMatcher) Match(v any) (vlatte.Result, error) {
	err := m.action(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("peek: %w", err)
	}

	return m.matcher.Match(v)
}

// Peek will return the expected of the given matcher, after executing the provided function.
// Peek can be used to check the matcher argument.
func Peek(matcher vlatte.Filter, action func(v any) error) vlatte.Filter {
	return &peekMatcher{matcher: matcher, action: action}
}

