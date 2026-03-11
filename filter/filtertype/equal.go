package filtertype

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/filter/internal/equalutil"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Equal passes when the value equals the expected value (lenient comparison).
type Equal struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *Equal) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	if equalutil.EqualValues(m.Expected, v, true) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Equal(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Equal) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eq", m.Expected)
}
