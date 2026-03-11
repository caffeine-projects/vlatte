package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Trim trims spaces and matches with the inner matcher.
type Trim struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *Trim) Match(arg vlatte.FilterArg) (bool, error) {
	v := arg.Value()
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("trim: type %T is not supported. accepted types: string", v))
	}

	result, err := m.Matcher.Match(vlatte.NewFilterArg(arg.Context(), strings.TrimSpace(txt)))
	if err != nil {
		return Error(fmt.Errorf("trim: %w", err))
	}

	if result {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Trim(", txt, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Trim) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("trim", Describe(m.Matcher))
}
