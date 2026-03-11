package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// UpperCase uppercases the value and matches with the inner matcher.
type UpperCase struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *UpperCase) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("upper: type %T is not supported. accepted types: string", v))
	}

	result, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), strings.ToUpper(txt)))
	if err != nil {
		return Error(fmt.Errorf("upper: %w", err))
	}

	if result {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Upper(", txt, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *UpperCase) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("uppercase", Describe(m.Matcher))
}
