package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// AnyOf passes when any of the inner matchers pass.
type AnyOf struct {
	Matchers []vlatte.Filter
}

// Name implements vlatte.Filter.
func (m *AnyOf) Name() string { return "AnyOf" }

// Match implements vlatte.Filter.
func (m *AnyOf) Match(arg vlatte.FilterArg) (ok bool, e error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			e = fmt.Errorf("any: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	for i, matcher := range m.Matchers {
		idx = i
		result, err := matcher.Match(arg)
		if err != nil {
			return false, fmt.Errorf("any: %d: %w", i, err)
		}

		if result {
			return Success()
		}
	}

	return Mismatch("Any() none matched")
}

// Describe implements descriptor.SelfDescriptive.
func (m *AnyOf) Describe() descriptor.Descriptor {
	d := make([]any, 0, len(m.Matchers))
	for _, v := range m.Matchers {
		if vv, ok := v.(descriptor.SelfDescriptive); ok {
			d = append(d, vv.Describe())
		}
	}

	return descriptor.MultiArgs("any", d)
}
