package match

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// eachMatcher applies the inner matcher to all items of the value (map/slice).
type eachMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *eachMatcher) Name() string { return "Each" }

// Match implements vlatte.Matcher.
func (m *eachMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	val := reflect.ValueOf(v)
	valType := reflect.TypeOf(v).Kind()

	switch valType {
	case reflect.Map:
		iterator := val.MapRange()

		for iterator.Next() {
			mv := iterator.Value().Interface()
			ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), mv))
			if err != nil {
				return failure(fmt.Errorf("each: %w", err))
			}

			if !ok {
				return mismatch("Each() item did not match")
			}
		}

		return success()

	case reflect.Slice, reflect.Array:
		for i := 0; i < val.Len(); i++ {
			entry := val.Index(i).Interface()
			ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), entry))
			if err != nil {
				return failure(fmt.Errorf("each: %w", err))
			}

			if !ok {
				return mismatch("Each() item did not match")
			}
		}

		return success()
	}

	return failure(fmt.Errorf("type %s is not supported. accepted types: map, array", valType.String()))
}

// Describe implements descriptor.SelfDescriptive.
func (m *eachMatcher) Describe() descriptor.Descriptor {
	if desc, ok := descriptor.IsSelfDescriptive(m.Matcher); ok {
		return descriptor.MultiArgs("each", desc)
	}

	return descriptor.KeyOnly("each")
}
