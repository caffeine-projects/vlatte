package match

import (
	"github.com/caffeine-projects/vlatte"
)

// anythingMatcher is a matcher that always passes.
type anythingMatcher struct{}

// Name implements vlatte.Matcher.
func (m *anythingMatcher) Name() string { return "Anything" }

// Match implements vlatte.Matcher.
func (m *anythingMatcher) Match(_ vlatte.MatcherArg) (bool, error) {
	return success()
}

// Describe implements vlatte.SelfDescriptive.
func (m *anythingMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyOnly("anything")
}
