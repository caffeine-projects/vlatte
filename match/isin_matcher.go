package match

import (
	"fmt"
	"reflect"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/equalutil"
)

// isInMatcher passes when the value is contained in the items slice.
type isInMatcher struct {
	Items any
}

// Name implements vlatte.Matcher.
func (m *isInMatcher) Name() string { return "IsIn" }

// Match implements vlatte.Matcher.
func (m *isInMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	typeOfItems := reflect.TypeOf(m.Items)
	kind := typeOfItems.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return failure(fmt.Errorf("is_in: it only works with slices. got: %v", typeOfItems))
	}

	valueOf := reflect.ValueOf(m.Items)

	for i := 0; i < valueOf.Len(); i++ {
		if equalutil.EqualValues(v, valueOf.Index(i).Interface(), false) {
			return success()
		}
	}

	return mismatch(fmt.Sprintf(
		"IsIn() Value %v is not contained in %v",
		v,
		m.Items,
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *isInMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("isIn", m.Items)
}
