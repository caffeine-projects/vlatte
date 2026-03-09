package filter

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type containsMatcher struct {
	expected any
}

func (m *containsMatcher) Match(v any) (bool, vlatte.Reason, error) {
	var eValue = reflect.ValueOf(m.expected)
	var vValue = reflect.ValueOf(v)
	var vType = reflect.TypeOf(v)
	if vType == nil {
		return false, ReasonError, errors.New("contains: unknown typeof value")
	}

	switch vType.Kind() {
	case reflect.String:
		if pass := strings.Contains(vValue.String(), eValue.String()); pass {
			return success()
		}

		goto ret

	case reflect.Map:
		keys := vValue.MapKeys()
		for i := 0; i < len(keys); i++ {
			if equalValues(keys[i].Interface(), m.expected, false) {
				return success()
			}
		}

		goto ret

	case reflect.Slice, reflect.Array:
		for i := 0; i < vValue.Len(); i++ {
			if equalValues(vValue.Index(i).Interface(), eValue.Interface(), false) {
				return success()
			}
		}
	}

ret:
	return mismatch(strings.Join([]string{"Contain(", mfmt.Stringify(m.expected), ") Got: ", mfmt.Stringify(v)}, ""))
}

func (m *containsMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("contains", m.expected)
}

// Contain passes when the expected value is contained in the incoming value from the request.
func Contain(expected any) vlatte.Filter {
	return &containsMatcher{expected: expected}
}

// Containf passes when the expected value is contained in the incoming value from the request.
// This is shorthand to format the expected value.
func Containf(format string, a ...any) vlatte.Filter {
	return &containsMatcher{expected: fmt.Sprintf(format, a...)}
}
