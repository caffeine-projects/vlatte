package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type xorMatcher struct {
	first  vlatte.Filter
	second vlatte.Filter
}

func (m *xorMatcher) Match(v any) (bool, vlatte.Reason, error) {
	aOK, aReason, err := m.first.Match(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("xor: first: %w", err)
	}

	bOK, bReason, err := m.second.Match(v)
	if err != nil {
		return false, ReasonError, fmt.Errorf("xor: second: %w", err)
	}

	if aOK != bOK {
		return true, ReasonNone, nil
	}

	var parts []string
	if !aOK {
		parts = append(parts, string(aReason))
	}
	if !bOK {
		parts = append(parts, string(bReason))
	}

	return false, vlatte.Reason(strings.Join([]string{"XOR()", strings.Join(parts, " - ")}, " ")), nil
}

func (m *xorMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("xor", describe(m.first), describe(m.second))
}

// XOR is an exclusive OR matcher.
func XOR(first vlatte.Filter, second vlatte.Filter) vlatte.Filter {
	return &xorMatcher{first: first, second: second}
}
