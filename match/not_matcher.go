package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// notMatcher negates the provided matcher.
type notMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *notMatcher) Name() string { return "Not" }

// Match implements vlatte.Matcher.
func (m *notMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	ok, err := m.Matcher.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("not: %w", err))
	}

	if !ok {
		return success()
	}

	return mismatch("Not() inner matcher matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *notMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", describe(m.Matcher))
}
