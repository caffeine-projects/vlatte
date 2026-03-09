package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type anythingMatcher struct {
}

func (m *anythingMatcher) Match(_ any) (vlatte.Result, error) {
	return vlatte.Result{Pass: true}, nil
}

func (m *anythingMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("anything")
}

// Anything is an empty matcher that always passes.
func Anything() vlatte.Filter {
	return &anythingMatcher{}
}
