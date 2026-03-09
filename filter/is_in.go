package filter

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type isContainedInMatcher struct {
	items any
}

func (m *isContainedInMatcher) Match(v any) (vlatte.Result, error) {
	typeOfItems := reflect.TypeOf(m.items)
	kind := typeOfItems.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return vlatte.Result{}, fmt.Errorf("is_in: it only works with slices. got: %v", typeOfItems)
	}

	valueOf := reflect.ValueOf(m.items)

	for i := 0; i < valueOf.Len(); i++ {
		if equalValues(v, valueOf.Index(i).Interface(), false) {
			return vlatte.Result{Pass: true}, nil
		}
	}

	return vlatte.Result{
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
func IsIn(items any) vlatte.Filter {
	return &isContainedInMatcher{items: items}
}
