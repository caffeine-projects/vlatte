package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type equalIgnoreCaseMatcher struct {
	expected string
}

func (m *equalIgnoreCaseMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt := ""
	if v != nil {
		var ok bool
		txt, ok = v.(string)
		if !ok {
			return false, ReasonError, fmt.Errorf("eqi: value must be a string. got: %T", v)
		}
	}

	if strings.EqualFold(m.expected, txt) {
		return success()
	}

	return mismatch(strings.Join([]string{"Eqi(", m.expected, ") Got: ", txt}, ""))
}

func (m *equalIgnoreCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eqi", m.expected)
}

// EqualIgnoreCase compares the expected value with the incoming request value ignoring the case.
func EqualIgnoreCase(expected string) vlatte.Filter {
	return &equalIgnoreCaseMatcher{expected: expected}
}

// EqualIgnoreCasef compares the expected value with the incoming request value ignoring the case.
// This is short-hand to format the expected value.
func EqualIgnoreCasef(format string, a ...any) vlatte.Filter {
	return EqualIgnoreCase(fmt.Sprintf(format, a...))
}

// Eqi is an alias to EqualIgnoreCase.
// It compares the expected value with the incoming request value ignoring the case.
func Eqi(expected string) vlatte.Filter {
	return &equalIgnoreCaseMatcher{expected: expected}
}
