package filtertype

import (
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// BePresent passes when the value is present (not nil and not zero).
type BePresent struct{}

// Match implements vlatte.Filter.
func (m *BePresent) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return false, vlatte.Reason("Present() value is nil"), nil
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
func (m *BePresent) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("present")
}
