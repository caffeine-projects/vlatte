package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// UpperCaseMatcher uppercases the value and matches with the inner matcher.
type UpperCaseMatcher struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *UpperCaseMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return Error(fmt.Errorf("upper: type %T is not supported. accepted types: string", v))
	}

	result, _, err := m.Matcher.Match(strings.ToUpper(txt))
	if err != nil {
		return Error(fmt.Errorf("upper: %w", err))
	}

	if result {
		return Success()
	}

	return Mismatch(strings.Join([]string{"Upper(", txt, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *UpperCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("uppercase", Describe(m.Matcher))
}
