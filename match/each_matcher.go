package match

import (
	"fmt"
	"maps"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// eachMatcher applies the inner matcher to every item of a list, or every member of an object.
type eachMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *eachMatcher) Name() string { return "Each" }

// Match implements vlatte.Matcher.
func (m *eachMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	items, ok := value.List(v)
	if !ok {
		members, isObject := value.Object(v)
		if !isObject {
			return mismatch("Each() Value is not a list or an object")
		}

		items = slices.Collect(maps.Values(members))
	}

	for _, item := range items {
		ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), item))
		if err != nil {
			return failure(fmt.Errorf("each: %w", err))
		}

		if !ok {
			return mismatch("Each() item did not match")
		}
	}

	return success()
}

// Describe implements vlatte.SelfDescriptive.
func (m *eachMatcher) Describe() vlatte.Descriptor {
	if desc, ok := vlatte.IsSelfDescriptive(m.Matcher); ok {
		return vlatte.DescribeMultiArgs("each", desc)
	}

	return vlatte.DescribeKeyOnly("each")
}
