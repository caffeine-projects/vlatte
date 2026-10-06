package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// greaterMatcher passes if the value is greater than the expected value, by value.Compare.
type greaterMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *greaterMatcher) Name() string { return "Greater" }

// Match implements vlatte.Matcher.
func (m *greaterMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if c, ok := value.Compare(v, m.Expected); ok && c > 0 {
		return success()
	}

	return mismatch(strings.Join([]string{"Gt(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *greaterMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gt", m.Expected)
}
