package filtertype

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// IsNil passes when the value is nil.
type IsNil struct{}

// Name implements vlatte.Filter.
func (m *IsNil) Name() string { return "Nil" }

// Match implements vlatte.Filter.
func (m *IsNil) Match(arg vlatte.FilterArg) (bool, error) {
	if arg.Value() == nil {
		return Success()
	}

	return Mismatch("Nil() Value is not nil")
}

// Describe implements descriptor.SelfDescriptive.
func (m *IsNil) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("nil")
}
