package filtertype

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// NilMatcher passes when the value is nil.
type NilMatcher struct{}

// Match implements vlatte.Filter.
func (m *NilMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return Success()
	}

	return Mismatch("Nil() Value is not nil")
}

// Describe implements descriptor.SelfDescriptive.
func (m *NilMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("nil")
}
