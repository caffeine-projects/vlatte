package filtertype

import (
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// BePresentMatcher passes when the value is present (not nil and not zero).
type BePresentMatcher struct{}

// Match implements vlatte.Filter.
func (m *BePresentMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return false, vlatte.Reason("Present() value is nil"), nil
	}

	val := reflect.ValueOf(v)

	switch val.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map, reflect.Struct, reflect.Interface:
		if !val.IsZero() {
			return internal.Success()
		}

		return internal.Mismatch("Present() Expected value to be present. Got: " + mfmt.Stringify(v))
	case reflect.Pointer:
		if !val.IsNil() {
			return internal.Success()
		}

		return internal.Mismatch("Present() Expected value to be present. Got: " + mfmt.Stringify(v))
	}

	return internal.Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *BePresentMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("present")
}
