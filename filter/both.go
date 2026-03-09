package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type bothMatcher struct {
	first  vlatte.Filter
	second vlatte.Filter
}

func (m *bothMatcher) Match(value any) (bool, vlatte.Reason, error) {
	r1OK, r1Reason, err := m.first.Match(value)
	if err != nil {
		return false, ReasonError, fmt.Errorf("both: first: %w", err)
	}

	r2OK, r2Reason, err := m.second.Match(value)
	if err != nil {
		return false, ReasonError, fmt.Errorf("both: first: %w", err)
	}

	if r1OK && r2OK {
		return true, ReasonNone, nil
	}

	parts := []string{}
	if !r1OK {
		parts = append(parts, string(r1Reason))
	}

	if !r2OK {
		parts = append(parts, string(r2Reason))
	}

	return false, vlatte.Reason(strings.Join([]string{"Both()", strings.Join(parts, " - ")}, " ")), nil
}

func (m *bothMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("both", describe(m.first), describe(m.second))
}

// Both passes when both the given matchers pass.
func Both(first vlatte.Filter, second vlatte.Filter) vlatte.Filter {
	return &bothMatcher{first: first, second: second}
}
