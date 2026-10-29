package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// lessOrEqualMatcher passes if the value is less than or equal to the expected value, by value.Compare.
type lessOrEqualMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *lessOrEqualMatcher) Name() string { return "LessOrEqual" }

// Match implements vlatte.Matcher.
func (m *lessOrEqualMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if c, ok := value.Compare(v, m.Expected); ok && c <= 0 {
		return success()
	}

	return mismatch(strings.Join([]string{"Lte(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *lessOrEqualMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("lte", m.Expected)
}
