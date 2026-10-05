package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// lowerCaseMatcher lowercases the value and matches with the inner matcher.
type lowerCaseMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *lowerCaseMatcher) Name() string { return "LowerCase" }

// Match implements vlatte.Matcher.
func (m *lowerCaseMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := v.(string)
	if !ok {
		return failure(fmt.Errorf("lower: type %T is not supported. accepted types: string", v))
	}

	result, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), strings.ToLower(txt)))
	if err != nil {
		return failure(fmt.Errorf("lower: %w", err))
	}

	if result {
		return success()
	}

	return mismatch("Lower() value did not match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *lowerCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lowercase", describe(m.Matcher))
}
