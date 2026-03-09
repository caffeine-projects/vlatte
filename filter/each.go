package filter

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type eachMatcher struct {
	matcher vlatte.Filter
}

func (m *eachMatcher) Match(v any) (vlatte.Result, error) {
	var val = reflect.ValueOf(v)
	var valType = reflect.TypeOf(v).Kind()

	switch valType {
	case reflect.Map:
		iterator := val.MapRange()

		for iterator.Next() {
			mv := iterator.Value().Interface()
			res, err := m.matcher.Match(mv)
			if err != nil {
				return vlatte.Result{}, fmt.Errorf("each: %w", err)
			}

			if !res.Pass {
				return vlatte.Result{
					Message: strings.
						Join([]string{
							"Each(",
							mfmt.Stringify(iterator.Key().Interface()), ":", mfmt.Stringify(mv),
							") ", res.Message}, "")}, nil
			}
		}

		return vlatte.Result{Pass: true}, nil

	case reflect.Slice, reflect.Array:
		for i := 0; i < val.Len(); i++ {
			entry := val.Index(i).Interface()
			res, err := m.matcher.Match(entry)
			if err != nil {
				return vlatte.Result{}, fmt.Errorf("each: %w", err)
			}

			if !res.Pass {
				return vlatte.Result{
					Message: strings.
						Join([]string{
							"Each(", mfmt.Stringify(entry), ") ", res.Message}, "")}, nil
			}
		}

		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{}, fmt.Errorf("type %s is not supported. accepted types: map, array", valType.String())
}

func (m *eachMatcher) Describe() descriptor.Descriptor {
	if desc, ok := descriptor.IsSelfDescriptive(m.matcher); ok {
		return descriptor.MultiArgs("each", desc)
	}

	return descriptor.KeyOnly("each")
}

// Each applies the given matcher on all items of the incoming request value.
// It works with slices and maps.
func Each(matcher vlatte.Filter) vlatte.Filter {
	return &eachMatcher{matcher: matcher}
}
