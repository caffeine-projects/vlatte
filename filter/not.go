package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type notMatcher struct {
	matcher vlatte.Filter
}

func (m *notMatcher) Match(v any) (bool, vlatte.Reason, error) {
	ok, reason, err := m.matcher.Match(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("not: %w", err)
	}

	if !ok {
		return true, ReasonNone, nil
	}

	return false, vlatte.Reason(fmt.Sprintf("!(%s)", reason)), nil
}

func (m *notMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", describe(m.matcher))
}

// Not negates the provided matcher.
func Not(matcher vlatte.Filter) vlatte.Filter {
	return &notMatcher{matcher: matcher}
}
