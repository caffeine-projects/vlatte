package filtertype

import (
	"errors"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// ContainsMatcher passes when the expected value is contained in the incoming value.
type ContainsMatcher struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *ContainsMatcher) Match(v any) (bool, vlatte.Reason, error) {
	var eValue = reflect.ValueOf(m.Expected)
	var vValue = reflect.ValueOf(v)
	var vType = reflect.TypeOf(v)
	if vType == nil {
		return internal.Error(errors.New("contains: unknown typeof value"))
	}

	switch vType.Kind() {
	case reflect.String:
		if pass := strings.Contains(vValue.String(), eValue.String()); pass {
			return internal.Success()
		}

		goto ret

	case reflect.Map:
		keys := vValue.MapKeys()
		for i := 0; i < len(keys); i++ {
			if internal.EqualValues(keys[i].Interface(), m.Expected, false) {
				return internal.Success()
			}
		}

		goto ret

	case reflect.Slice, reflect.Array:
		for i := 0; i < vValue.Len(); i++ {
			if internal.EqualValues(vValue.Index(i).Interface(), eValue.Interface(), false) {
				return internal.Success()
			}
		}
	}

ret:
	return internal.Mismatch(strings.Join([]string{"Contain(", mfmt.Stringify(m.Expected), ") Got: ", mfmt.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *ContainsMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("contains", m.Expected)
}
