package match

import (
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// presentMatcher passes when the value is present (not nil and not zero).
type presentMatcher struct{}

// Name implements vlatte.Matcher.
func (m *presentMatcher) Name() string { return "Present" }

// Match implements vlatte.Matcher.
func (m *presentMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return mismatch("Present() value is nil")
	}

	val := reflect.ValueOf(v)

	switch val.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map, reflect.Struct, reflect.Interface:
		if !val.IsZero() {
			return success()
		}

		return mismatch("Present() Expected value to be present. Got: " + anyconv.Stringify(v))
	case reflect.Pointer:
		if !val.IsNil() {
			return success()
		}

		return mismatch("Present() Expected value to be present. Got: " + anyconv.Stringify(v))
	}

	return success()
}

// Describe implements vlatte.SelfDescriptive.
func (m *presentMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyOnly("present")
}
