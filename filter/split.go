package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type splitMatcher struct {
	separator string
	matcher   vlatte.Filter
}

func (m *splitMatcher) Match(v any) (bool, vlatte.Reason, error) {
	txt, ok := v.(string)
	if !ok {
		return false, ReasonError, fmt.Errorf("split: type %T is not supported. accepted types: string", v)
	}

	result, _, err := m.matcher.Match(strings.Split(txt, m.separator))
	if err != nil {
		return false, ReasonError, fmt.Errorf("split: %w", err)
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Split(", m.separator, ")"}, ""))
}

func (m *splitMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("split", m.separator, describe(m.matcher))
}

// Split splits the incoming request value text using the separator parameter and
// test each element with the provided matcher.
func Split(separator string, matcher vlatte.Filter) vlatte.Filter {
	return &splitMatcher{separator: separator, matcher: matcher}
}
