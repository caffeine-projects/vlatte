package match

import (
	"fmt"
	"slices"
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

	paired, err := pairUp(len(m.Expected), len(items), func(w, g int) (bool, error) {
		if inner, ok := m.Expected[w].(vlatte.Matcher); ok {
			return inner.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), items[g]))
		}

		return value.Equal(m.Expected[w], items[g]), nil
	})
	if err != nil {
		return failure(fmt.Errorf("itemsMatch: %w", err))
	}

	if !paired {
		return mismatch("ItemsMatch() Items doesn't match")
	}

	return success()
}

// Describe implements descriptor.SelfDescriptive. Items that are matchers describe as hasExactly, whose items are
// matcher specs: an item that is a value is written as the Equal matcher that compares it.
func (m *itemsMatchMatcher) Describe() descriptor.Descriptor {
	if !slices.ContainsFunc(m.Expected, isMatcher) {
		return descriptor.SingleArg("itemsMatch", m.Expected)
	}

	specs := make([]any, len(m.Expected))
	for i, item := range m.Expected {
		inner, ok := item.(vlatte.Matcher)
		if !ok {
			inner = Equal(item)
		}

		specs[i] = describe(inner)
	}

	return descriptor.SingleArg("hasExactly", specs)
}

func isMatcher(v any) bool {
	_, ok := v.(vlatte.Matcher)
	return ok
}
