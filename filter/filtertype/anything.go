package filtertype

import (
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Anything is a matcher that always passes.
type Anything struct{}

// Match implements vlatte.Filter.
func (m *Anything) Match(_ any) (bool, error) {
	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *Anything) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("anything")
}
