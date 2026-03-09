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

// Match implements vlatte.Filter.
func (m *Not) Match(v any) (bool, vlatte.Reason, error) {
	ok, reason, err := m.Matcher.Match(v)
	if err != nil {
		return Error(fmt.Errorf("not: %w", err))
	}

	if !ok {
		return Success()
	}

	return Mismatch(fmt.Sprintf("!(%s)", reason))
}

// Describe implements descriptor.SelfDescriptive.
func (m *Not) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", Describe(m.Matcher))
}
