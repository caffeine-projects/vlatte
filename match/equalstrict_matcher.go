package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// equalStrictMatcher passes when the value equals the expected value without reading text, by value.Same.
type equalStrictMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *equalStrictMatcher) Name() string { return "EqualStrict" }

// Match implements vlatte.Matcher.
func (m *equalStrictMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if value.Same(m.Expected, v) {
		return success()
	}

	return mismatch(strings.Join([]string{"StrictEq(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *equalStrictMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeMultiArgs("eqs", m.Expected)
}
