package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// LowerCaseMatcher lowercases the value and matches with the inner matcher.
type LowerCaseMatcher struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *LowerCaseMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("lower: type %T is not supported. accepted types: string", v))
	}

	result, _, err := m.Matcher.Match(strings.ToLower(txt))
	if err != nil {
		return Error(fmt.Errorf("lower: %w", err))
	}

	if result {
		return Success()
	}

	return Mismatch("Lower() value did not match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *LowerCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lowercase", Describe(m.Matcher))
}
