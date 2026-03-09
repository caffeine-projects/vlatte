package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type upperCaseMatcher struct {
	matcher vlatte.Filter
}

func (m *upperCaseMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return false, ReasonError, fmt.Errorf("upper: type %T is not supported. accepted types: string", v)
	}

	result, _, err := m.matcher.Match(strings.ToUpper(txt))
	if err != nil {
		return false, ReasonError, fmt.Errorf("upper: %w", err)
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Upper(", txt, ")"}, ""))
}

func (m *upperCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("uppercase", describe(m.matcher))
}

// ToUpper uppers the case of the incoming request value before submitting it to provided matcher.
func ToUpper(matcher vlatte.Filter) vlatte.Filter {
	return &upperCaseMatcher{matcher: matcher}
}
