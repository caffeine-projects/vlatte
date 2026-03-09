package filter

import (
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type bePresentMatcher struct {
}

func (m *bePresentMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return false, vlatte.Reason("Present() value is nil"), nil
	}

	val := reflect.ValueOf(v)

	switch val.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map, reflect.Struct, reflect.Interface:
		if !val.IsZero() {
			return success()
		}

		return mismatch("Present() Expected value to be present. Got: " + mfmt.Stringify(v))
	case reflect.Pointer:
		if !val.IsNil() {
			return success()
		}

		return mismatch("Present() Expected value to be present. Got: " + mfmt.Stringify(v))
	}

	return success()
}

func (m *bePresentMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("present")
}

// Present checks if the incoming request value contains a value that is not nil or the zero value for the argument type.
func Present() vlatte.Filter {
	return &bePresentMatcher{}
}
