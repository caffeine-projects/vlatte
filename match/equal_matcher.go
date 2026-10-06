package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// equalMatcher passes when the value equals the expected value, by value.Equal.
type equalMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *equalMatcher) Name() string { return "Equal" }

// Match implements vlatte.Matcher.
func (m *equalMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if value.Equal(m.Expected, v) {
		return success()
	}

	return mismatch(strings.Join([]string{"Equal(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eq", m.Expected)
}
