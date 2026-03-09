package filter

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type equalStrictMatcher struct {
	expected any
}

func (m *equalStrictMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if reflect.DeepEqual(m.expected, v) {
		return success()
	}

	return mismatch(strings.Join([]string{"StrictEq(", mfmt.Stringify(m.expected), ") Got: ", mfmt.Stringify(v)}, ""))
}

func (m *equalStrictMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqs", m.expected)
}

// StrictEqual strictly compares the expected value with incoming request values, considering value and type.
func StrictEqual(expected any) vlatte.Filter {
	return &equalStrictMatcher{expected: expected}
}

// StrictEqualf strictly compares the expected value with incoming request values, considering value and type.
// This is short-hand to format the expected value.
func StrictEqualf(format string, a ...any) vlatte.Filter {
	return &equalStrictMatcher{expected: fmt.Sprintf(format, a...)}
}

// Eqs strictly compares the expected value with incoming request values, considering value and type.
func Eqs(expected any) vlatte.Filter {
	return &equalStrictMatcher{expected: expected}
}
