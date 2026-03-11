package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Either passes when either of the two matchers pass.
type Either struct {
	First  vlatte.Filter
	Second vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *Either) Match(arg vlatte.FilterArg) (bool, error) {
	r1OK, err := m.First.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("either: first: %w", err))
	}

	r2OK, err := m.Second.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("either: second: %w", err))
	}

	if r1OK || r2OK {
		return Success()
	}

	return Mismatch("Either() neither matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *Either) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("either", Describe(m.First), Describe(m.Second))
}
