package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// anyOfMatcher passes when any of the inner matchers pass.
type anyOfMatcher struct {
	Matchers []vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *anyOfMatcher) Name() string { return "AnyOf" }

// Match implements vlatte.Matcher.
func (m *anyOfMatcher) Match(arg vlatte.MatcherArg) (ok bool, e error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			e = fmt.Errorf("any: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	for i, matcher := range m.Matchers {
		idx = i
		result, err := matcher.Match(arg)
		if err != nil {
			return false, fmt.Errorf("any: %d: %w", i, err)
		}

		if result {
			return success()
		}
	}

	return mismatch("Any() none matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *anyOfMatcher) Describe() descriptor.Descriptor {
	d := make([]any, 0, len(m.Matchers))
	for _, v := range m.Matchers {
		if vv, ok := v.(descriptor.SelfDescriptive); ok {
			d = append(d, vv.Describe())
		}
	}

	return descriptor.MultiArgs("any", d)
}
