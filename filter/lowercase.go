package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/std/descriptor"
)

type lowerCaseMatcher struct {
	matcher Filter
}

func (m *lowerCaseMatcher) Match(v any) (Result, error) {
	txt, ok := v.(string)
	if !ok {
		return Result{}, fmt.Errorf("lower: type %T is not supported. accepted types: string", v)
	}

	result, err := m.matcher.Match(strings.ToLower(txt))
	if err != nil {
		return Result{}, fmt.Errorf("lower: %w", err)
	}

	if result.Pass {
		return Result{Pass: true}, nil
	}

	return Result{
		Message: "Lower() " + result.Message,
	}, nil
}

func (m *lowerCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lowercase", describe(m.matcher))
}

// ToLower lowers the incoming request value case value before submitting it to provided matcher.
func ToLower(matcher Filter) Filter {
	return &lowerCaseMatcher{matcher: matcher}
}
