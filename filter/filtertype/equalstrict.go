package filtertype

import (
	"reflect"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/anyconv"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// EqualStrict passes when the value strictly equals the expected (reflect.DeepEqual).
type EqualStrict struct {
	Expected any
}

// Name implements vlatte.Filter.
func (m *EqualStrict) Name() string { return "EqualStrict" }

// Match implements vlatte.Filter.
func (m *EqualStrict) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	if reflect.DeepEqual(m.Expected, v) {
		return Success()
	}

	return Mismatch(strings.Join([]string{"StrictEq(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EqualStrict) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("eqs", m.Expected)
}
