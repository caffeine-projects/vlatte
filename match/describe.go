package match

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// describe returns the descriptor for the given filter, or a default for non-self-descriptive filters.
func describe(m vlatte.Matcher) any {
	if sd, ok := m.(descriptor.SelfDescriptive); ok {
		return sd.Describe()
	}
	return (&anythingMatcher{}).Describe()
}
