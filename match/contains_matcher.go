package match

import (
	"errors"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
	"github.com/caffeine-projects/vlatte/match/internal/equalutil"
)

// containsMatcher passes when the expected value is contained in the incoming value.
type containsMatcher struct {
	Expected any
}

// Name implements vlatte.Matcher.
func (m *containsMatcher) Name() string { return "Contains" }

// Match implements vlatte.Matcher.
func (m *containsMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	eValue := reflect.ValueOf(m.Expected)
	vValue := reflect.ValueOf(v)
	vType := reflect.TypeOf(v)
	if vType == nil {
		return failure(errors.New("contains: unknown typeof value"))
	}

	switch vType.Kind() {
	case reflect.String:
		if pass := strings.Contains(vValue.String(), eValue.String()); pass {
			return success()
		}

		goto ret

	case reflect.Map:
		keys := vValue.MapKeys()
		for i := range keys {
			if equalutil.EqualValues(keys[i].Interface(), m.Expected, false) {
				return success()
			}
		}

		goto ret

	case reflect.Slice, reflect.Array:
		for i := 0; i < vValue.Len(); i++ {
			if equalutil.EqualValues(vValue.Index(i).Interface(), eValue.Interface(), false) {
				return success()
			}
		}
	}

ret:
	return mismatch(strings.Join([]string{"Contain(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *containsMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("contains", m.Expected)
}
