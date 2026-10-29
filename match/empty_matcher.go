package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// emptyMatcher passes when the value is nil, or has no length, by value.Len.
type emptyMatcher struct{}

// Name implements vlatte.Matcher.
func (m *emptyMatcher) Name() string { return "Empty" }

// Match implements vlatte.Matcher.
func (m *emptyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return success()
	}

	if n, ok := value.Len(v); ok && n == 0 {
		return success()
	}

	return mismatch(strings.Join([]string{"Empty(", anyconv.Stringify(v), ")"}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *emptyMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyOnly("empty")
}
