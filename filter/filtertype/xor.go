package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// XorMatcher is an exclusive OR matcher.
type XorMatcher struct {
	First  vlatte.Filter
	Second vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *XorMatcher) Match(v any) (bool, vlatte.Reason, error) {
	aOK, aReason, err := m.First.Match(v)
	if err != nil {
		return Error(fmt.Errorf("xor: first: %w", err))
	}

	bOK, bReason, err := m.Second.Match(v)
	if err != nil {
		return Error(fmt.Errorf("xor: second: %w", err))
	}

	if aOK != bOK {
		return Success()
	}

	var parts []string
	if !aOK {
		parts = append(parts, string(aReason))
	}
	if !bOK {
		parts = append(parts, string(bReason))
	}

	return Mismatch(strings.Join([]string{"XOR()", strings.Join(parts, " - ")}, " "))
}

// Describe implements descriptor.SelfDescriptive.
func (m *XorMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("xor", Describe(m.First), Describe(m.Second))
}
