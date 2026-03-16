package filtertype

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Some passes when at least one element matches the inner matcher.
type Some struct {
	Matcher vlatte.Filter
}

// Name implements vlatte.Filter.
func (m *Some) Name() string { return "Some" }

// Match implements vlatte.Filter.
func (m *Some) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return Mismatch("Some() Expected some values to match but got nil")
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return Error(fmt.Errorf("some: matcher only works with slices. got: %v", typeOfV))
	}

	vv := reflect.ValueOf(v)
	if vv.IsZero() {
		return Mismatch("Some() Expected some values to match but got zero")
	}

	for i := 0; i < vv.Len(); i++ {
		ok, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), arg.Explain(), vv.Index(i).Interface()))
		if err != nil {
			return Error(fmt.Errorf("some: %w", err))
		}

		if ok {
			return Success()
		}
	}

	return Mismatch("Some() no element matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *Some) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("some", Describe(m.Matcher))
}
