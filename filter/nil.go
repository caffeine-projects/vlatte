package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type nilMatcher struct {
}

func (m *nilMatcher) Match(v any) (bool, vlatte.Reason, error) {
	if v == nil {
		return success()
	}

	return mismatch("Nil() Value is not nil")
}

func (m *nilMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("nil")
}

// IsNil passes if the incoming request value is nil.
func IsNil() vlatte.Filter {
	return &nilMatcher{}
}
