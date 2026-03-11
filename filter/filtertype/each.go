package filtertype

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Each applies the inner matcher to all items of the value (map/slice).
type Each struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *Each) Match(v any) (bool, error) {
	var val = reflect.ValueOf(v)
	var valType = reflect.TypeOf(v).Kind()

	switch valType {
	case reflect.Map:
		iterator := val.MapRange()

		for iterator.Next() {
			mv := iterator.Value().Interface()
			ok, err := m.Matcher.Match(mv)
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
			ok, err := m.Matcher.Match(entry)
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
