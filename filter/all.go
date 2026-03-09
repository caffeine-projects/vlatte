package filter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type allOfMatcher struct {
	matchers []vlatte.Filter
}

func (m *allOfMatcher) Match(v any) (ok bool, reason vlatte.Reason, err error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			err = fmt.Errorf("all: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	mismatches := make([]string, 0, len(m.matchers))

	for i, matcher := range m.matchers {
		idx = i
		res, r, err := matcher.Match(v)
		if err != nil {
			return false, ReasonError, fmt.Errorf("all: %d: %w", i, err)
		}

		if !res {
			mismatches = append(mismatches, string(r))
		}
	}

	if len(mismatches) == 0 {
		return true, ReasonNone, nil
	}

	return false, vlatte.Reason(strings.Join(
		[]string{"All(", strconv.Itoa(len(m.matchers)), ")\n", mfmt.Indent(strings.Join(mismatches, "\n"))},
		"",
	)), nil
}

func (m *allOfMatcher) Describe() descriptor.Descriptor {
	d := make([]any, 0, len(m.matchers))
	for _, v := range m.matchers {
		if vv, ok := v.(descriptor.SelfDescriptive); ok {
			d = append(d, vv.Describe())
		}
	}

	return descriptor.MultiArgs("all", d)
}

// All matches when all the given matchers pass.
// Example:
//
//	All(Equal("test"), EqualIgnoreCase("test"), Contain("tes"))
func All(matchers ...vlatte.Filter) vlatte.Filter {
	return &allOfMatcher{matchers: matchers}
}
