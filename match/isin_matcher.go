package match

import (
	"fmt"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// isInMatcher passes when the value equals one of the items.
type isInMatcher struct {
	Items []any
}

// Name implements vlatte.Matcher.
func (m *isInMatcher) Name() string { return "IsIn" }

// Match implements vlatte.Matcher.
func (m *isInMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if slices.ContainsFunc(m.Items, func(item any) bool { return value.Equal(item, v) }) {
		return success()
	}

	return mismatch(fmt.Sprintf("IsIn() Value %v is not contained in %v", v, m.Items))
}

// Describe implements vlatte.SelfDescriptive.
func (m *isInMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("isIn", m.Items)
}
