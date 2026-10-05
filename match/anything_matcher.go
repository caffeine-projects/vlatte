package match

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// anythingMatcher is a matcher that always passes.
type anythingMatcher struct{}

// Name implements vlatte.Matcher.
func (m *anythingMatcher) Name() string { return "Anything" }

// Match implements vlatte.Matcher.
func (m *anythingMatcher) Match(_ vlatte.MatcherArg) (bool, error) {
	return success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *anythingMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("anything")
}
