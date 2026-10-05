package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
	"github.com/caffeine-projects/vlatte/match/internal/equalutil"
)

// equalMatcher passes when the value equals the expected value (lenient comparison).
type equalMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *equalMatcher) Name() string { return "Equal" }

// Match implements vlatte.Matcher.
func (m *equalMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if equalutil.EqualValues(m.Expected, v, true) {
		return success()
	}

	return mismatch(strings.Join([]string{"Equal(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eq", m.Expected)
}
