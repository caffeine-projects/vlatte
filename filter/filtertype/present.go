package filtertype

import (
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Present passes when the value is present (not nil and not zero).
type Present struct{}

// Name implements vlatte.Filter.
func (m *Present) Name() string { return "Present" }

// Match implements vlatte.Filter.
func (m *Present) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return Mismatch("Present() value is nil")
	}

	val := reflect.ValueOf(v)

	switch val.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map, reflect.Struct, reflect.Interface:
		if !val.IsZero() {
			return Success()
		}

		return Mismatch("Present() Expected value to be present. Got: " + anyconv.Stringify(v))
	case reflect.Pointer:
		if !val.IsNil() {
			return Success()
		}

		return Mismatch("Present() Expected value to be present. Got: " + anyconv.Stringify(v))
	}

	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *Present) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("present")
}
