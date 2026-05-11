package filtertype

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// Describe returns the descriptor for the given filter, or a default for non-self-descriptive filters.
func Describe(m vlatte.Filter) any {
	if sd, ok := m.(descriptor.SelfDescriptive); ok {
		return sd.Describe()
	}
	return (&Anything{}).Describe()
}
