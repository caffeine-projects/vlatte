package match

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/equalutil"
)

// itemsMatchMatcher passes when the value slice has the same elements as expected (order-independent).
type itemsMatchMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *itemsMatchMatcher) Name() string { return "ItemsMatch" }

// Match implements vlatte.Matcher.
func (m *itemsMatchMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	a := reflect.ValueOf(v)
	b := reflect.ValueOf(m.Expected)

	aLen := a.Len()
	bLen := b.Len()

	if aLen != bLen {
		return mismatch(strings.Join(
			[]string{"ItemsMatch() Expected value len to be ", strconv.Itoa(bLen), ". Got: ", strconv.Itoa(aLen)},
			"",
		))
	}

	var extraA, extraB []any

	visited := make([]bool, bLen)

	for i := range aLen {
		element := a.Index(i).Interface()
		found := false

		for j := range bLen {
			if visited[j] {
				continue
			}

			if equalutil.EqualValues(b.Index(j).Interface(), element, false) {
				visited[j] = true
				found = true
				break
			}
		}

		if !found {
			extraA = append(extraA, element)
		}
	}

	for j := range bLen {
		if visited[j] {
			continue
		}

		extraB = append(extraB, b.Index(j).Interface())
	}

	if len(extraA) == 0 && len(extraB) == 0 {
		return success()
	}

	return mismatch("ItemsMatch() Items doesn't match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *itemsMatchMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("itemsMatch", m.Expected)
}
