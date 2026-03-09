package filtertype

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// AllOfMatcher passes when all the given matchers pass.
type AllOfMatcher struct {
	Matchers []vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *AllOfMatcher) Match(v any) (ok bool, reason vlatte.Reason, err error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			err = fmt.Errorf("all: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	mismatches := make([]string, 0, len(m.Matchers))

	for i, matcher := range m.Matchers {
		idx = i
		res, r, err := matcher.Match(v)
		if err != nil {
			return internal.Error(fmt.Errorf("all: %d: %w", i, err))
		}

		if !res {
			mismatches = append(mismatches, string(r))
		}
	}

	if len(mismatches) == 0 {
		return internal.Success()
	}

	return internal.Mismatch(strings.Join(
		[]string{"All(", strconv.Itoa(len(m.Matchers)), ")\n", mfmt.Indent(strings.Join(mismatches, "\n"))},
		"",
	))
}

// Describe implements descriptor.SelfDescriptive.
func (m *AllOfMatcher) Describe() descriptor.Descriptor {
	d := make([]any, 0, len(m.Matchers))
	for _, v := range m.Matchers {
		if vv, ok := v.(descriptor.SelfDescriptive); ok {
			d = append(d, vv.Describe())
		}
	}

	return descriptor.MultiArgs("all", d)
}
