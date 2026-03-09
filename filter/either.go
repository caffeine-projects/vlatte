package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type eitherMatcher struct {
	first  vlatte.Filter
	second vlatte.Filter
}

func (m *eitherMatcher) Match(v any) (bool, vlatte.Reason, error) {
	r1OK, r1Reason, err := m.first.Match(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("either: first: %w", err)
	}

	r2OK, r2Reason, err := m.second.Match(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("either: second: %w", err)
	}

	if r1OK || r2OK {
		return true, ReasonNone, nil
	}

	parts := []string{}
	if !r1OK {
		parts = append(parts, string(r1Reason))
	}
	if !r2OK {
		parts = append(parts, string(r2Reason))
	}

	return false, vlatte.Reason(strings.Join([]string{"Either()", strings.Join(parts, " - ")}, " ")), nil
}

func (m *eitherMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("either", describe(m.first), describe(m.second))
}

// Either passes when any of the two given matchers pass.
func Either(first vlatte.Filter, second vlatte.Filter) vlatte.Filter {
	return &eitherMatcher{first: first, second: second}
}
