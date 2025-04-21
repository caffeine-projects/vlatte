package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type notMatcher struct {
	matcher Filter
}

func (m *notMatcher) Match(v any) (Result, error) {
	result, err := m.matcher.Match(v)
	if err != nil {
		return Result{}, fmt.Errorf("not: %w", err)
	}

	if !result.Pass {
		return Result{Pass: true}, nil
	}

	return Result{
		Message: fmt.Sprintf("!(%s)", result.Message),
	}, nil
}

func (m *notMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", describe(m.matcher))
}

// Not negates the provided matcher.
func Not(matcher Filter) Filter {
	return &notMatcher{matcher: matcher}
}
