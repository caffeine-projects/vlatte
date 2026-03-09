package filter

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type someMatcher struct {
	matcher vlatte.Filter
}

func (m *someMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return mismatch("Some() Expected some values to match but got nil")
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return false, ReasonError, fmt.Errorf("some: matcher only works with slices. got: %v", typeOfV)
	}

	vv := reflect.ValueOf(v)
	if vv.IsZero() {
		return mismatch("Some() Expected some values to match but got zero")
	}

	length := vv.Len()
	messages := make([]string, 0, length)

	for i := 0; i < length; i++ {
		ok, reason, err := m.matcher.Match(vv.Index(i).Interface())
		if err != nil {
			return false, ReasonError, fmt.Errorf("some: %w", err)
		}

		if ok {
			return true, ReasonNone, nil
		}

		messages = append(messages, string(reason))
	}

	return false, vlatte.Reason(fmt.Sprintf(
		"Some()\n%s",
		mfmt.Indent(strings.Join(messages, "\n")),
	)), nil
}

func (m *someMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("some", describe(m.matcher))
}

// Some will use the given matcher to test whether at least one element in the request value passes.
func Some(matcher vlatte.Filter) vlatte.Filter {
	return &someMatcher{matcher: matcher}
}
