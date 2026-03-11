package filtertype

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Nil passes when the value is nil.
type Nil struct{}

// Match implements vlatte.Filter.
func (m *Nil) Match(arg vlatte.FilterArg) (bool, error) {
	if arg.Value() == nil {
		return Success()
	}

	return Mismatch("Nil() Value is not nil")
}

// Describe implements descriptor.SelfDescriptive.
func (m *Nil) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("nil")
}
