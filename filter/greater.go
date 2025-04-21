package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/filter/internal/mconv"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type greaterMatcher struct {
	expected float64
}

func (m *greaterMatcher) Match(v any) (Result, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return Result{}, fmt.Errorf(
			"gt: type %T is not supported. the value must be compatible with float64. %w", v, err)
	}

	if vv > m.expected {
		return Result{Pass: true}, nil
	}

	return Result{
		Message: strings.Join([]string{"Gt(", mfmt.Stringify(m.expected), ") Value is ", mfmt.Stringify(v)}, ""),
	}, nil
}

func (m *greaterMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gt", m.expected)
}

// GreaterThan passes if the incoming request value is greater than the given value.
func GreaterThan(expected float64) Filter {
	return &greaterMatcher{expected: expected}
}

// Gt passes if the incoming request value is greater than the given value.
func Gt(expected float64) Filter {
	return &greaterMatcher{expected: expected}
}
