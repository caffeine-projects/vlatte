package match

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// falsyMatcher passes when the value reads as false, by value.Bool.
type falsyMatcher struct{}

// Name implements vlatte.Matcher.
func (m *falsyMatcher) Name() string { return "Falsy" }

// Match implements vlatte.Matcher.
func (m *falsyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	if b, ok := value.Bool(arg.Value()); ok && !b {
		return success()
	}

	return mismatch("Falsy() Expected false but it is actually true")
}

// Describe implements vlatte.SelfDescriptive.
func (m *falsyMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyOnly("falsy")
}
