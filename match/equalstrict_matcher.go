package match

import (
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// equalStrictMatcher passes when the value strictly equals the expected (reflect.DeepEqual).
type equalStrictMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *equalStrictMatcher) Name() string { return "EqualStrict" }

// Match implements vlatte.Matcher.
func (m *equalStrictMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if reflect.DeepEqual(m.Expected, v) {
		return success()
	}

	return mismatch(strings.Join([]string{"StrictEq(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalStrictMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqs", m.Expected)
}
