package filtertype

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// ItemMatcher matches a specific array/slice item with the inner matcher.
type ItemMatcher struct {
	Index   int
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *ItemMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if m.Index < 0 {
		return Error(fmt.Errorf("item: index must greater or equal to 0. got %d", m.Index))
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return Error(fmt.Errorf("item: %d: it only works with slices. got: %T", m.Index, v))
	}

	vv := reflect.ValueOf(v)
	if vv.Len() == 0 {
		return Mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.Index), ") Empty array"}, ""))
	}

	ok, _, err := m.Matcher.Match(vv.Index(m.Index).Interface())
	if err != nil {
		return Error(fmt.Errorf("item: %d: %w", m.Index, err))
	}

	if ok {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.Index), ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *ItemMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("item", m.Index, Describe(m.Matcher))
}
