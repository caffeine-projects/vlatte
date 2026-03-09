package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type notMatcher struct {
	matcher vlatte.Filter
}

func (m *notMatcher) Match(v any) (vlatte.Result, error) {
	result, err := m.matcher.Match(v)
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("not: %w", err)
	}

	if !result.Pass {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{
		Message: fmt.Sprintf("!(%s)", result.Message),
	}, nil
}

func (m *notMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", describe(m.matcher))
}

// Not negates the provided matcher.
func Not(matcher vlatte.Filter) vlatte.Filter {
	return &notMatcher{matcher: matcher}
}
