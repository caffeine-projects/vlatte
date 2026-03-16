package filtertype

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/equalutil"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// IsIn passes when the value is contained in the items slice.
type IsIn struct {
	Items any
}

// Name implements vlatte.Filter.
func (m *IsIn) Name() string { return "IsIn" }

// Match implements vlatte.Filter.
func (m *IsIn) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	typeOfItems := reflect.TypeOf(m.Items)
	kind := typeOfItems.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return Error(fmt.Errorf("is_in: it only works with slices. got: %v", typeOfItems))
	}

	valueOf := reflect.ValueOf(m.Items)

	for i := 0; i < valueOf.Len(); i++ {
		if equalutil.EqualValues(v, valueOf.Index(i).Interface(), false) {
			return Success()
		}
	}

	return Mismatch(fmt.Sprintf(
		"IsIn() Value %v is not contained in %v",
		v,
		m.Items,
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *IsIn) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("isIn", m.Items)
}
