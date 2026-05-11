package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// GreaterOrEqual passes if the value is greater than or equal to the expected value.
type GreaterOrEqual struct {
	Expected float64
}

// Name implements vlatte.Filter.
func (m *GreaterOrEqual) Name() string { return "GreaterOrEqual" }

// Match implements vlatte.Filter.
func (m *GreaterOrEqual) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
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
func (m *GreaterOrEqual) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("gte", m.Expected)
}
