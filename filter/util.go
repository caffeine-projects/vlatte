package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// success represents a successful match with no specific reason.
func success() (bool, vlatte.Reason, error) {
	return true, ReasonNone, nil
}

// mismatch represents a failed match with a descriptive reason string.
func mismatch(message string) (bool, vlatte.Reason, error) {
	return false, vlatte.Reason(message), nil
}

func describe(m vlatte.Filter) any {
	if sd, ok := m.(descriptor.SelfDescriptive); ok {
		return sd.Describe()
	}

	return Anything().(descriptor.SelfDescriptive).Describe()
}
