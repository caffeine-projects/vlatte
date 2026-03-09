package filtertype

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// AnythingMatcher is a matcher that always passes.
type AnythingMatcher struct{}

// Match implements vlatte.Filter.
func (m *AnythingMatcher) Match(_ any) (bool, vlatte.Reason, error) {
	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *AnythingMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("anything")
}
