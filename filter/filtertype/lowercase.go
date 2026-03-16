package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// LowerCase lowercases the value and matches with the inner matcher.
type LowerCase struct {
	Matcher vlatte.Filter
}

// Name implements vlatte.Filter.
func (m *LowerCase) Name() string { return "LowerCase" }

// Match implements vlatte.Filter.
func (m *LowerCase) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("lower: type %T is not supported. accepted types: string", v))
	}

	result, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), arg.Explain(), strings.ToLower(txt)))
	if err != nil {
		return Error(fmt.Errorf("lower: %w", err))
	}

	if result {
		return Success()
	}

	return Mismatch("Lower() value did not match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *LowerCase) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lowercase", Describe(m.Matcher))
}
