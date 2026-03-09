package filter

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/mfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type anyOfMatcher struct {
	matchers []vlatte.Filter
}

func (m *anyOfMatcher) Match(v any) (r vlatte.Result, e error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			e = fmt.Errorf("any: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	mismatches := make([]string, 0, len(m.matchers))

	for i, matcher := range m.matchers {
		idx = i
		result, err := matcher.Match(v)
		if err != nil {
			return vlatte.Result{}, fmt.Errorf("any: %d: %w", i, err)
		}

		if !result.Pass {
			mismatches = append(mismatches, result.Message)
			continue
		}

		return success(), nil
	}

	return vlatte.Result{Message: strings.Join([]string{"Any(", strconv.Itoa(len(m.matchers)), ")\n", mfmt.Indent(strings.Join(mismatches, "\n"))}, "")}, nil
}

func (m *anyOfMatcher) Describe() descriptor.Descriptor {
	d := make([]any, 0, len(m.matchers))
	for _, v := range m.matchers {
		if vv, ok := v.(descriptor.SelfDescriptive); ok {
			d = append(d, vv.Describe())
		}
	}

	return descriptor.MultiArgs("any", d)
}

// Any matches when any of the given matchers pass.
// Example:
//
//	Any(Equal("test"), EqualIgnoreCase("TEST"), Contain("tes"))
func Any(matchers ...vlatte.Filter) vlatte.Filter {
	return &anyOfMatcher{matchers: matchers}
}
