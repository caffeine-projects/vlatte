package filter

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte/std/descriptor"
)

type isContainedInMatcher struct {
	items any
}

func (m *isContainedInMatcher) Match(v any) (Result, error) {
	typeOfItems := reflect.TypeOf(m.items)
	kind := typeOfItems.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return Result{}, fmt.Errorf("is_in: it only works with slices. got: %v", typeOfItems)
	}

	valueOf := reflect.ValueOf(m.items)

	for i := 0; i < valueOf.Len(); i++ {
		if equalValues(v, valueOf.Index(i).Interface(), false) {
			return Result{Pass: true}, nil
		}
	}

	return Result{
		Message: fmt.Sprintf(
			"IsIn() Value %v is not contained in %v",
			v,
			m.items),
	}, nil
}

func (m *isContainedInMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("isIn", m.items)
}

// IsIn checks if the incoming request value is in the given items.
// Parameter items must be a slice.
func IsIn(items any) Filter {
	return &isContainedInMatcher{items: items}
}
