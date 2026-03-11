package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// LessOrEqual passes if the value is less than or equal to the expected value.
type LessOrEqual struct {
	Expected float64
}

// Match implements vlatte.Filter.
func (m *LessOrEqual) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return Error(fmt.Errorf(
			"lte: type %T is not supported. value must be compatible with float64. %w",
			v,
			err,
		))
	}

	if vv <= m.Expected {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Lte(", anyconv.Stringify(m.Expected), ") Value is ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *LessOrEqual) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lte", m.Expected)
}
