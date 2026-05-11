package filtertype

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// Anything is a matcher that always passes.
type Anything struct{}

// Name implements vlatte.Filter.
func (m *Anything) Name() string { return "Anything" }

// Match implements vlatte.Filter.
func (m *Anything) Match(_ vlatte.FilterArg) (bool, error) {
	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *Anything) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("anything")
}
