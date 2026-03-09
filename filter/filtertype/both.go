package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// BothMatcher passes when both inner matchers pass.
type BothMatcher struct {
	First  vlatte.Filter
	Second vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *BothMatcher) Match(value any) (bool, vlatte.Reason, error) {
	r1OK, r1Reason, err := m.First.Match(value)
	if err != nil {
		return internal.Error(fmt.Errorf("both: first: %w", err))
	}

	r2OK, r2Reason, err := m.Second.Match(value)
	if err != nil {
		return internal.Error(fmt.Errorf("both: first: %w", err))
	}

	if r1OK && r2OK {
		return internal.Success()
	}

	parts := []string{}
	if !r1OK {
		parts = append(parts, string(r1Reason))
	}

	if !r2OK {
		parts = append(parts, string(r2Reason))
	}

	return internal.Mismatch(strings.Join([]string{"Both()", strings.Join(parts, " - ")}, " "))
}

// Describe implements descriptor.SelfDescriptive.
func (m *BothMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("both", Describe(m.First), Describe(m.Second))
}
