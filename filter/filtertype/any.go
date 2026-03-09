package filtertype

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/filterfmt"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// AnyOf passes when any of the inner matchers pass.
type AnyOf struct {
	Matchers []vlatte.Filter
}

// Match implements vlatte.Filter.
func (m *AnyOf) Match(v any) (ok bool, reason vlatte.Reason, e error) {
	var idx int
	defer func() {
		if recovery := recover(); recovery != nil {
			e = fmt.Errorf("any: matcher[%d]: panic: %v", idx, recovery)
		}
	}()

	mismatches := make([]string, 0, len(m.Matchers))

	for i, matcher := range m.Matchers {
		idx = i
		result, r, err := matcher.Match(v)
		if err != nil {
			return Error(fmt.Errorf("any: %d: %w", i, err))
		}

		if !result {
			mismatches = append(mismatches, string(r))
			continue
		}

		return Success()
	}

	return Mismatch(strings.Join(
		[]string{"Any(", strconv.Itoa(len(m.Matchers)), ")\n", filterfmt.Indent(strings.Join(mismatches, "\n"))},
		"",
	))
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
