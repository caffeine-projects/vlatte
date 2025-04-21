package filter

import "github.com/caffeine-projects/vlatte/std/descriptor"

type anythingMatcher struct {
}

func (m *anythingMatcher) Match(_ any) (Result, error) {
	return Result{Pass: true}, nil
}

func (m *anythingMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("anything")
}

// Anything is an empty matcher that always passes.
func Anything() Matcher {
	return &anythingMatcher{}
}
