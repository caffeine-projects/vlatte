package filtertype

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// EitherMatcher passes when either of the two matchers pass.
type EitherMatcher struct {
	First  vlatte.Filter
	Second vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *EitherMatcher) Match(v any) (bool, vlatte.Reason, error) {
	r1OK, r1Reason, err := m.First.Match(v)
	if err != nil {
		return internal.Error(fmt.Errorf("either: first: %w", err))
	}

	r2OK, r2Reason, err := m.Second.Match(v)
	if err != nil {
		return internal.Error(fmt.Errorf("either: second: %w", err))
	}

	if r1OK || r2OK {
		return internal.Success()
	}

	parts := []string{}
	if !r1OK {
		parts = append(parts, string(r1Reason))
	}
	if !r2OK {
		parts = append(parts, string(r2Reason))
	}

	return internal.Mismatch(strings.Join([]string{"Either()", strings.Join(parts, " - ")}, " "))
}

// Describe implements descriptor.SelfDescriptive.
func (m *EitherMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("either", Describe(m.First), Describe(m.Second))
}
