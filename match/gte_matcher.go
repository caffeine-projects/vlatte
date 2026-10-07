package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// greaterOrEqualMatcher passes if the value is greater than or equal to the expected value, by value.Compare.
type greaterOrEqualMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *greaterOrEqualMatcher) Name() string { return "GreaterOrEqual" }

// Match implements vlatte.Matcher.
func (m *greaterOrEqualMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if c, ok := value.Compare(v, m.Expected); ok && c >= 0 {
		return success()
	}

	return mismatch(strings.Join([]string{"Gte(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *greaterOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gte", m.Expected)
}
