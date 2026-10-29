package match

import (
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// lenMatcher passes when the value's length, by value.Len, equals the expected length.
type lenMatcher struct {
	Length int
}

// Name implements vlatte.Matcher.
func (m *lenMatcher) Name() string { return "Len" }

// Match implements vlatte.Matcher.
func (m *lenMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	n, ok := value.Len(arg.Value())
	if ok && n == m.Length {
		return success()
	}

	return mismatch(strings.Join([]string{"Len(", strconv.Itoa(m.Length), ") Got: ", strconv.Itoa(n)}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *lenMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("len", m.Length)
}
