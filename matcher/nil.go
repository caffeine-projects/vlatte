package matcher

import "github.com/caffeine-projects/vlatte/kcore/descriptor"

type nilMatcher struct {
}

func (m *nilMatcher) Match(v any) (Result, error) {
	if v == nil {
		return Result{Pass: true}, nil
	}

	return Result{Message: "Nil() Value is not nil"}, nil
}

func (m *nilMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("nil")
}

// IsNil passes if the incoming request value is nil.
func IsNil() Matcher {
	return &nilMatcher{}
}
