package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// GreaterOrEqualMatcher passes if the value is greater than or equal to the expected value.
type GreaterOrEqualMatcher struct {
	Expected float64
}

// Match implements vlatte.Filter.
func (m *GreaterOrEqualMatcher) Match(v any) (bool, vlatte.Reason, error) {
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return Error(fmt.Errorf(
			"gte: type %T is not supported. the value must be compatible with float64. %w",
			v,
			err,
		))
	}

	if vv >= m.Expected {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Gte(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *GreaterOrEqualMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gte", m.Expected)
}
