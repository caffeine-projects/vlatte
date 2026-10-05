package match

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// itemMatcher matches a specific array/slice item with the inner matcher.
type itemMatcher struct {
	Index   int
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *itemMatcher) Name() string { return "Item" }

// Match implements vlatte.Matcher.
func (m *itemMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if m.Index < 0 {
		return failure(fmt.Errorf("item: index must greater or equal to 0. got %d", m.Index))
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return failure(fmt.Errorf("item: %d: it only works with slices. got: %T", m.Index, v))
	}

	vv := reflect.ValueOf(v)
	if vv.Len() == 0 {
		return mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.Index), ") Empty array"}, ""))
	}

	ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), vv.Index(m.Index).Interface()))
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
