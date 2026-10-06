package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// someMatcher passes when an item of a list matches the inner matcher.
type someMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *someMatcher) Name() string { return "Some" }

// Match implements vlatte.Matcher.
func (m *someMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	items, ok := value.List(arg.Value())
	if !ok {
		return mismatch("Some() Value is not a list")
	}

	for _, item := range items {
		ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), item))
		if err != nil {
			return failure(fmt.Errorf("some: %w", err))
		}

		if ok {
			return success()
		}
	}

	return mismatch("Some() no element matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *someMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("some", describe(m.Matcher))
}
