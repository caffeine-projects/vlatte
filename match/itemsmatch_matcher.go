package match

import (
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// itemsMatchMatcher passes when the value is a list holding the expected items, in any order.
type itemsMatchMatcher struct {
	Expected []any
}

// Name implements vlatte.Matcher.
func (m *itemsMatchMatcher) Name() string { return "ItemsMatch" }

// Match implements vlatte.Matcher.
func (m *itemsMatchMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	items, ok := value.List(arg.Value())
	if !ok {
		return mismatch("ItemsMatch() Value is not a list")
	}

	if len(items) != len(m.Expected) {
		return mismatch(strings.Join(
			[]string{"ItemsMatch() Expected value len to be ", strconv.Itoa(len(m.Expected)), ". Got: ", strconv.Itoa(len(items))},
			"",
		))
	}

	if !pairUp(m.Expected, items) {
		return mismatch("ItemsMatch() Items doesn't match")
	}

	return success()
}

// pairUp reports whether every item of want pairs with an item of got of its own, equal to it.
// Equal isn't transitive, as "1.0" and "1" both equal 1 but not each other, so a greedy pairing could miss one
// that exists: this finds a maximum matching instead, with augmenting paths.
func pairUp(want, got []any) bool {
	equal := make([][]bool, len(want))
	for w := range want {
		equal[w] = make([]bool, len(got))
		for g := range got {
			equal[w][g] = value.Equal(want[w], got[g])
		}
	}

	pair := make([]int, len(got)) // the item of want each item of got is paired with, or -1
	for g := range pair {
		pair[g] = -1
	}

	var augment func(w int, seen []bool) bool

	augment = func(w int, seen []bool) bool {
		for g := range got {
			if seen[g] || !equal[w][g] {
				continue
			}

			seen[g] = true

			if pair[g] < 0 || augment(pair[g], seen) {
				pair[g] = w
				return true
			}
		}

		return false
	}

	for w := range want {
		if !augment(w, make([]bool, len(got))) {
			return false
		}
	}

	return true
}

// Describe implements descriptor.SelfDescriptive.
func (m *itemsMatchMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("itemsMatch", m.Expected)
}
