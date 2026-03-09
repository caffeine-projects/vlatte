package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// NotMatcher negates the provided matcher.
type NotMatcher struct {
	Matcher vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *NotMatcher) Match(v any) (bool, vlatte.Reason, error) {
	ok, reason, err := m.Matcher.Match(v)
	if err != nil {
		return internal.Error(fmt.Errorf("not: %w", err))
	}

	if !ok {
		return internal.Success()
	}

	return internal.Mismatch(fmt.Sprintf("!(%s)", reason))
}

// Describe implements descriptor.SelfDescriptive.
func (m *NotMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("not", Describe(m.Matcher))
}
