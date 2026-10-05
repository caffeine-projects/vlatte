package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// eitherMatcher passes when either of the two matchers pass.
type eitherMatcher struct {
	First  vlatte.Matcher
	Second vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *eitherMatcher) Name() string { return "Either" }

// Match implements vlatte.Matcher.
func (m *eitherMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	r1OK, err := m.First.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("either: first: %w", err))
	}

	r2OK, err := m.Second.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("either: second: %w", err))
	}

	if r1OK || r2OK {
		return success()
	}

	return mismatch("Either() neither matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *eitherMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("either", describe(m.First), describe(m.Second))
}
