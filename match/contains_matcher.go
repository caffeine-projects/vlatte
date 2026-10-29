package match

import (
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// containsMatcher passes when the value holds the expected value: text, an item of a list or a key of an object.
type containsMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *containsMatcher) Name() string { return "Contains" }

// Match implements vlatte.Matcher.
func (m *containsMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	if m.holds(v) {
		return success()
	}

	return mismatch(strings.Join([]string{"Contain(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *containsMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("contains", m.Expected)
}

func (m *containsMatcher) holds(v any) bool {
	if text, ok := value.Text(v); ok {
		expected, isText := value.Text(m.Expected)
		return isText && strings.Contains(text, expected)
	}

	if items, ok := value.List(v); ok {
		return slices.ContainsFunc(items, func(item any) bool { return value.Equal(item, m.Expected) })
	}

	if members, ok := value.Object(v); ok {
		for key := range members {
			if value.Equal(key, m.Expected) {
				return true
			}
		}
	}

	return false
}
