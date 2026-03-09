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

// GreaterMatcher passes if the value is greater than the expected value.
type GreaterMatcher struct {
	Expected float64
}

// Match implements vlatte.Filter.
func (m *GreaterMatcher) Match(v any) (bool, vlatte.Reason, error) {
	vv, err := mconv.ConvToFloat64(v)
	if err != nil {
		return internal.Error(fmt.Errorf(
			"gt: type %T is not supported. the value must be compatible with float64. %w", v, err))
	}

	if vv > m.Expected {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join([]string{"Gt(", mfmt.Stringify(m.Expected), ") Value is ", mfmt.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *GreaterMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gt", m.Expected)
}
