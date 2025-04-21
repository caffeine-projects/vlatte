package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type splitMatcher struct {
	separator string
	matcher   Filter
}

func (m *splitMatcher) Match(v any) (Result, error) {
	txt, ok := v.(string)
	if !ok {
		return Result{}, fmt.Errorf("split: type %T is not supported. accepted types: string", v)
	}

	result, err := m.matcher.Match(strings.Split(txt, m.separator))
	if err != nil {
		return Result{}, fmt.Errorf("split: %w", err)
	}

	if result.Pass {
		return Result{Pass: true}, err
	}

	return Result{Message: strings.Join([]string{"Split(", m.separator, ") ", result.Message}, "")}, nil
}

func (m *splitMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("split", m.separator, describe(m.matcher))
}

// Split splits the incoming request value text using the separator parameter and
// test each element with the provided matcher.
func Split(separator string, matcher Filter) Filter {
	return &splitMatcher{separator: separator, matcher: matcher}
}
