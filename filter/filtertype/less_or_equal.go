package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mconv"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// LessOrEqualMatcher passes if the value is less than or equal to the expected value.
type LessOrEqualMatcher struct {
	Expected float64
}

// Match implements vlatte.Filter.
func (m *LessOrEqualMatcher) Match(v any) (bool, vlatte.Reason, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return internal.Error(fmt.Errorf(
			"lte: type %T is not supported. value must be compatible with float64. %w",
			v,
			err,
		))
	}

	if vv <= m.Expected {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join([]string{"Lte(", mfmt.Stringify(m.Expected), ") Value is ", mfmt.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *LessOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lte", m.Expected)
}
