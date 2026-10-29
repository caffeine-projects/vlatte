package match

import (
	"github.com/caffeine-projects/vlatte"
)

// isNilMatcher passes when the value is nil.
type isNilMatcher struct{}

// Name implements vlatte.Matcher.
func (m *isNilMatcher) Name() string { return "Nil" }

// Match implements vlatte.Matcher.
func (m *isNilMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	if arg.Value() == nil {
		return success()
	}

	return mismatch("Nil() Value is not nil")
}

// Describe implements vlatte.SelfDescriptive.
func (m *isNilMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyOnly("nil")
}
