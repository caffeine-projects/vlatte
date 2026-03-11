package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Less passes if the value is less than the expected value.
type Less struct {
	Expected float64
}

// Match implements vlatte.Filter.
func (m *Less) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	vv, err := anyconv.ToFloat64(v)
	if err != nil {
		return Error(fmt.Errorf("lt: type %T is not supported. value must be compatible with float64. %w", v, err))
	}

	if vv < m.Expected {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Lt(", anyconv.Stringify(m.Expected), ") Value is ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Less) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lt", m.Expected)
}
