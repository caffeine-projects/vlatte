package filter

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type itemMatcher struct {
	index   int
	matcher vlatte.Filter
}

func (m *itemMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if m.index < 0 {
		return false, ReasonError, fmt.Errorf("item: index must greater or equal to 0. got %d", m.index)
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return false, ReasonError, fmt.Errorf("item: %d: it only works with slices. got: %T", m.index, v)
	}

	vv := reflect.ValueOf(v)
	if vv.Len() == 0 {
		return mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.index), ") Empty array"}, ""))
	}

	ok, _, err := m.matcher.Match(vv.Index(m.index).Interface())
	if err != nil {
		return false, ReasonError, fmt.Errorf("item: %d: %w", m.index, err)
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"Item(", strconv.Itoa(m.index), ")"}, ""))
}

func (m *itemMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("item", m.index, describe(m.matcher))
}

// Item matches a specific array item from the incoming request value.
func Item(index int, matcher vlatte.Filter) vlatte.Filter {
	return &itemMatcher{index: index, matcher: matcher}
}
