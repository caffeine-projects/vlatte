package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type trimMatcher struct {
	matcher vlatte.Filter
}

func (m *trimMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return false, ReasonError, fmt.Errorf("trim: type %T is not supported. accepted types: string", v)
	}

	result, _, err := m.matcher.Match(strings.TrimSpace(txt))
	if err != nil {
		return false, ReasonError, fmt.Errorf("trim: %w", err)
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Trim(", txt, ")"}, ""))
}

func (m *trimMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("trim", describe(m.matcher))
}

// Trim trims' spaces of the incoming request value before submitting it to the provided matcher.
func Trim(matcher vlatte.Filter) vlatte.Filter {
	return &trimMatcher{matcher: matcher}
}
