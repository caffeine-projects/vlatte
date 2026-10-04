package filtertype

import (
	"errors"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/filter/internal/equalutil"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// Contains passes when the expected value is contained in the incoming value.
type Contains struct {
	Expected any
}

// Name implements vlatte.Filter.
func (m *Contains) Name() string { return "Contains" }

// Match implements vlatte.Filter.
func (m *Contains) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	eValue := reflect.ValueOf(m.Expected)
	vValue := reflect.ValueOf(v)
	vType := reflect.TypeOf(v)
	if vType == nil {
		return Error(errors.New("contains: unknown typeof value"))
	}

	switch vType.Kind() {
	case reflect.String:
		if pass := strings.Contains(vValue.String(), eValue.String()); pass {
			return Success()
		}

		goto ret

	case reflect.Map:
		keys := vValue.MapKeys()
		for i := range keys {
			if equalutil.EqualValues(keys[i].Interface(), m.Expected, false) {
				return Success()
			}
		}

		goto ret

	case reflect.Slice, reflect.Array:
		for i := 0; i < vValue.Len(); i++ {
			if equalutil.EqualValues(vValue.Index(i).Interface(), eValue.Interface(), false) {
				return Success()
			}
		}
	}

ret:
	return Mismatch(strings.Join([]string{"Contain(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Contains) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("contains", m.Expected)
}
