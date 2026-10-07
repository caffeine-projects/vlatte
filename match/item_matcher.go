package match

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// itemMatcher applies the inner matcher to the item of a list at an index.
type itemMatcher struct {
	Index   int
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *itemMatcher) Name() string { return "Item" }

// Match implements vlatte.Matcher.
func (m *itemMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	items, isList := value.List(arg.Value())
	if !isList || m.Index >= len(items) {
		return mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.Index), ") No such item"}, ""))
	}

	ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), items[m.Index]))
	if err != nil {
		return failure(fmt.Errorf("item: %d: %w", m.Index, err))
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.Index), ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *itemMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("item", m.Index, describe(m.Matcher))
}
