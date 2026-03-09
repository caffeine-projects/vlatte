package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mconv"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type greaterOrEqualMatcher struct {
	expected float64
}

func (m *greaterOrEqualMatcher) Match(v any) (bool, vlatte.Reason, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf(
			"gte: type %T is not supported. the value must be compatible with float64. %w",
			v,
			err,
		)
	}

	if vv >= m.expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Gte(", mfmt.Stringify(m.expected), ") Got: ", mfmt.Stringify(v)}, ""))
}

func (m *greaterOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gte", m.expected)
}

// GreaterThanOrEqual passes if the incoming request value is greater than or equal to the given value.
func GreaterThanOrEqual(expected float64) vlatte.Filter {
	return &greaterOrEqualMatcher{expected: expected}
}

// Gte passes if the incoming request value is greater than or equal to the given value.
func Gte(expected float64) vlatte.Filter {
	return &greaterOrEqualMatcher{expected: expected}
}
