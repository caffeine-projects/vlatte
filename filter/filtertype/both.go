package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Both passes when both inner matchers pass.
type Both struct {
	First  vlatte.Filter
	Second vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *Both) Match(arg vlatte.FilterArg) (bool, error) {
	r1OK, err := m.First.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("both: first: %w", err))
	}

	r2OK, err := m.Second.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("both: second: %w", err))
	}

	if r1OK && r2OK {
		return Success()
	}

	return Mismatch("Both() one or both did not match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *Both) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("both", Describe(m.First), Describe(m.Second))
}
