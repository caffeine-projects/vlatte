package filtertype

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// AllOf passes when all the given matchers pass.
type AllOf struct {
	Matchers []vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *AllOf) Match(arg vlatte.FilterArg) (ok bool, err error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			err = fmt.Errorf("all: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	var failed bool
	for i, matcher := range m.Matchers {
		idx = i
		res, err := matcher.Match(arg)
		if err != nil {
			return false, fmt.Errorf("all: %d: %w", i, err)
		}
		if !res {
			failed = true
		}
	}
	if failed {
		return Mismatch("All() one or more matchers failed")
	}
	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *AllOf) Describe() descriptor.Descriptor {
	d := make([]any, 0, len(m.Matchers))
	for _, v := range m.Matchers {
		if vv, ok := v.(descriptor.SelfDescriptive); ok {
			d = append(d, vv.Describe())
		}
	}

	return descriptor.MultiArgs("all", d)
}
