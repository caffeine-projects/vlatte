package match

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// truthyMatcher passes when the value reads as true, by value.Bool.
type truthyMatcher struct{}

// Name implements vlatte.Matcher.
func (m *truthyMatcher) Name() string { return "Truthy" }

// Match implements vlatte.Matcher.
func (m *truthyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	if b, ok := value.Bool(arg.Value()); ok && b {
		return success()
	}

	return mismatch("Truthy() Expected true but it is actually false")
}

// Describe implements descriptor.SelfDescriptive.
func (m *truthyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("truthy")
}
