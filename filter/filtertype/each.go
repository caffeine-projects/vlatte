package filtertype

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// Each applies the inner matcher to all items of the value (map/slice).
type Each struct {
	Matcher vlatte.Filter
}

// Name implements vlatte.Filter.
func (m *Each) Name() string { return "Each" }

// Match implements vlatte.Filter.
func (m *Each) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	val := reflect.ValueOf(v)
	valType := reflect.TypeOf(v).Kind()

	switch valType {
	case reflect.Map:
		iterator := val.MapRange()

		for iterator.Next() {
			mv := iterator.Value().Interface()
			ok, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), arg.Explain(), mv))
			if err != nil {
				return Error(fmt.Errorf("each: %w", err))
			}

			if !ok {
				return Mismatch("Each() item did not match")
			}
		}

		return Success()

	case reflect.Slice, reflect.Array:
		for i := 0; i < val.Len(); i++ {
			entry := val.Index(i).Interface()
			ok, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), arg.Explain(), entry))
			if err != nil {
				return Error(fmt.Errorf("each: %w", err))
			}

			if !ok {
				return Mismatch("Each() item did not match")
			}
		}

		return Success()
	}

	return Error(fmt.Errorf("type %s is not supported. accepted types: map, array", valType.String()))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Each) Describe() descriptor.Descriptor {
	if desc, ok := descriptor.IsSelfDescriptive(m.Matcher); ok {
		return descriptor.MultiArgs("each", desc)
	}

	return descriptor.KeyOnly("each")
}
