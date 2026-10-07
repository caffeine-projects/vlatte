package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// lessMatcher passes if the value is less than the expected value, by value.Compare.
type lessMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *lessMatcher) Name() string { return "Less" }

// Match implements vlatte.Matcher.
func (m *lessMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if c, ok := value.Compare(v, m.Expected); ok && c < 0 {
		return success()
	}

	return mismatch(strings.Join([]string{"Lt(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *lessMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lt", m.Expected)
}
