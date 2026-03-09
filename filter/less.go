package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mconv"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type lessMatcher struct {
	expected float64
}

func (m *lessMatcher) Match(v any) (bool, vlatte.Reason, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("lt: type %T is not supported. value must be compatible with float64. %w", v, err)
	}

	if vv < m.expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Lt(", mfmt.Stringify(m.expected), ") Value is ", mfmt.Stringify(v)}, ""))
}

func (m *lessMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lt", m.expected)
}

// LessThan passes if the incoming request value is lower than the given value.
func LessThan(expected float64) vlatte.Filter {
	return &lessMatcher{expected: expected}
}

// Lt passes if the incoming request value is lower than the given value.
func Lt(expected float64) vlatte.Filter {
	return &lessMatcher{expected: expected}
}
