package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mconv"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type lessOrEqualMatcher struct {
	expected float64
}

func (m *lessOrEqualMatcher) Match(v any) (bool, vlatte.Reason, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf(
			"lte: type %T is not supported. value must be compatible with float64. %w",
			v,
			err,
		)
	}

	if vv <= m.expected {
		return success()
	}

	return mismatch(strings.Join([]string{"Lte(", mfmt.Stringify(m.expected), ") Value is ", mfmt.Stringify(v)}, ""))
}

func (m *lessOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lte", m.expected)
}

// LessThanOrEqual passes if the incoming request value is lower than or equal to the given value.
func LessThanOrEqual(expected float64) vlatte.Filter {
	return &lessOrEqualMatcher{expected: expected}
}

// Lte passes if the incoming request value is lower than or equal to the given value.
func Lte(expected float64) vlatte.Filter {
	return &lessOrEqualMatcher{expected: expected}
}
