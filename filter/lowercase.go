package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type lowerCaseMatcher struct {
	matcher vlatte.Filter
}

func (m *lowerCaseMatcher) Match(v any) (vlatte.Result, error) {
	txt, ok := v.(string)
	if !ok {
		return vlatte.Result{}, fmt.Errorf("lower: type %T is not supported. accepted types: string", v)
	}

	result, err := m.matcher.Match(strings.ToLower(txt))
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("lower: %w", err)
	}

	if result.Pass {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{
		Message: "Lower() " + result.Message,
	}, nil
}

func (m *lowerCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("lowercase", describe(m.matcher))
}

// ToLower lowers the incoming request value case value before submitting it to provided matcher.
func ToLower(matcher vlatte.Filter) vlatte.Filter {
	return &lowerCaseMatcher{matcher: matcher}
}
