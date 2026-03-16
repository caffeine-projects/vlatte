package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Xor is an exclusive OR matcher.
type Xor struct {
	First  vlatte.Filter
	Second vlatte.Filter
}

// Name implements vlatte.Filter.
func (m *Xor) Name() string { return "Xor" }

// Match implements vlatte.Filter.
func (m *Xor) Match(arg vlatte.FilterArg) (bool, error) {
	aOK, err := m.First.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("xor: first: %w", err))
	}

	bOK, err := m.Second.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("xor: second: %w", err))
	}

	if aOK != bOK {
		return Success()
	}

	return Mismatch("XOR() both matched or both failed")
}

// Describe implements descriptor.SelfDescriptive.
func (m *Xor) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("xor", Describe(m.First), Describe(m.Second))
}
