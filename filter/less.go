package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/std/descriptor"
	"github.com/caffeine-projects/vlatte/filter/internal/mconv"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
)

type lessMatcher struct {
	expected float64
}

func (m *lessMatcher) Match(v any) (Result, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return Result{}, fmt.Errorf("lt: type %T is not supported. value must be compatible with float64. %w", v, err)
	}

	if vv < m.expected {
		return Result{Pass: true}, nil
	}

	return Result{
		Message: strings.Join([]string{"Lt(", mfmt.Stringify(m.expected), ") Value is ", mfmt.Stringify(v)}, ""),
	}, nil
}

func (m *lessMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lt", m.expected)
}

// LessThan passes if the incoming request value is lower than the given value.
func LessThan(expected float64) Matcher {
	return &lessMatcher{expected: expected}
}

// Lt passes if the incoming request value is lower than the given value.
func Lt(expected float64) Matcher {
	return &lessMatcher{expected: expected}
}
