package matcher

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/kcore/descriptor"
	"github.com/caffeine-projects/vlatte/matcher/internal/mconv"
	"github.com/caffeine-projects/vlatte/matcher/internal/mfmt"
)

type lessOrEqualMatcher struct {
	expected float64
}

func (m *lessOrEqualMatcher) Match(v any) (Result, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return Result{}, fmt.Errorf(
			"lte: type %T is not supported. value must be compatible with float64. %w",
			v,
			err,
		)
	}

	if vv <= m.expected {
		return Result{Pass: true}, nil
	}

	return Result{
			Message: strings.
				Join([]string{"Lte(", mfmt.Stringify(m.expected), ") Value is ", mfmt.Stringify(v)}, "")},
		nil
}

func (m *lessOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lte", m.expected)
}

// LessThanOrEqual passes if the incoming request value is lower than or equal to the given value.
func LessThanOrEqual(expected float64) Matcher {
	return &lessOrEqualMatcher{expected: expected}
}

// Lte passes if the incoming request value is lower than or equal to the given value.
func Lte(expected float64) Matcher {
	return &lessOrEqualMatcher{expected: expected}
}
