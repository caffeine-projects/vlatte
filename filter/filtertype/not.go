package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Not negates the provided matcher.
type Not struct {
	Matcher vlatte.Filter
}

// Name implements vlatte.Filter.
func (m *Not) Name() string { return "Not" }

// Match implements vlatte.Filter.
func (m *Not) Match(arg vlatte.FilterArg) (bool, error) {
	ok, err := m.Matcher.Match(arg)
	if err != nil {
		return Error(fmt.Errorf("not: %w", err))
	}

	if !ok {
		return Success()
	}

	return Mismatch("Not() inner matcher matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *Not) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", Describe(m.Matcher))
}
