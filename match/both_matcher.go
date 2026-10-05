package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// bothMatcher passes when both inner matchers pass.
type bothMatcher struct {
	First  vlatte.Matcher
	Second vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *bothMatcher) Name() string { return "Both" }

// Match implements vlatte.Matcher.
func (m *bothMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	r1OK, err := m.First.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("both: first: %w", err))
	}

	r2OK, err := m.Second.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("both: second: %w", err))
	}

	if r1OK && r2OK {
		return success()
	}

	return mismatch("Both() one or both did not match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *bothMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("both", describe(m.First), describe(m.Second))
}
