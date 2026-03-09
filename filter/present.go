package filter

import (
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type bePresentMatcher struct {
}

func (m *bePresentMatcher) Match(v any) (vlatte.Result, error) {
	if v == nil {
		return vlatte.Result{}, nil
	}

	val := reflect.ValueOf(v)

	switch val.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map, reflect.Struct, reflect.Interface:
		if !val.IsZero() {
			return success(), nil
		}

		return vlatte.Result{Message: "Present() Expected value to be present. Got: " + mfmt.Stringify(v)}, nil
	case reflect.Pointer:
		if !val.IsNil() {
			return success(), nil
		}

		return vlatte.Result{Message: "Present() Expected value to be present. Got: " + mfmt.Stringify(v)}, nil
	}

	return vlatte.Result{Pass: true}, nil
}

func (m *bePresentMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("present")
}

// Present checks if the incoming request value contains a value that is not nil or the zero value for the argument type.
func Present() vlatte.Filter {
	return &bePresentMatcher{}
}
