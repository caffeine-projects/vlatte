package filtertype

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/filterfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// SomeMatcher passes when at least one element matches the inner matcher.
type SomeMatcher struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *SomeMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return Mismatch("Some() Expected some values to match but got nil")
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return Error(fmt.Errorf("some: matcher only works with slices. got: %v", typeOfV))
	}

	vv := reflect.ValueOf(v)
	if vv.IsZero() {
		return Mismatch("Some() Expected some values to match but got zero")
	}

	length := vv.Len()
	messages := make([]string, 0, length)

	for i := 0; i < length; i++ {
		ok, reason, err := m.Matcher.Match(vv.Index(i).Interface())
		if err != nil {
			return Error(fmt.Errorf("some: %w", err))
		}

		if ok {
			return Success()
		}

		messages = append(messages, string(reason))
	}

	return Mismatch(fmt.Sprintf(
		"Some()\n%s",
		filterfmt.Indent(strings.Join(messages, "\n")),
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *SomeMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("some", Describe(m.Matcher))
}
