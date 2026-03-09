package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type nilMatcher struct {
}

func (m *nilMatcher) Match(v any) (vlatte.Result, error) {
	if v == nil {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{Message: "Nil() Value is not nil"}, nil
}

func (m *nilMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("nil")
}

// IsNil passes if the incoming request value is nil.
func IsNil() vlatte.Filter {
	return &nilMatcher{}
}
