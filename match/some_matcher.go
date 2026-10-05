package match

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// someMatcher passes when at least one element matches the inner matcher.
type someMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *someMatcher) Name() string { return "Some" }

// Match implements vlatte.Matcher.
func (m *someMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return mismatch("Some() Expected some values to match but got nil")
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return failure(fmt.Errorf("some: matcher only works with slices. got: %v", typeOfV))
	}

	vv := reflect.ValueOf(v)
	if vv.IsZero() {
		return mismatch("Some() Expected some values to match but got zero")
	}

	for i := 0; i < vv.Len(); i++ {
		ok, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), vv.Index(i).Interface()))
		if err != nil {
			return failure(fmt.Errorf("some: %w", err))
		}

		if ok {
			return success()
		}
	}

	return mismatch("Some() no element matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *someMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("some", describe(m.Matcher))
}
