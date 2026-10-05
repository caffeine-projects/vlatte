package match

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// lenMatcher passes when the value's length equals the expected length.
type lenMatcher struct {
	Length int
}

// Name implements vlatte.Matcher.
func (m *lenMatcher) Name() string { return "Len" }

// Match implements vlatte.Matcher.
func (m *lenMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	value := reflect.ValueOf(v)
	actual := value.Len()
	if actual == m.Length {
		return success()
	}

	return mismatch(strings.Join(
		[]string{"Len(", strconv.Itoa(m.Length), ") Got: ", strconv.Itoa(actual)},
		"",
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *lenMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("len", m.Length)
}
