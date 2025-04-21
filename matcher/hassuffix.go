package matcher

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/std/descriptor"
)

type hasSuffixMatcher struct {
	suffix string
}

func (m *hasSuffixMatcher) Match(v any) (Result, error) {
	txt, ok := v.(string)
	if !ok {
		return Result{}, fmt.Errorf("has_suffix: it only works with string type. got %T", v)
	}

	if strings.HasSuffix(txt, m.suffix) {
		return Result{Pass: true}, nil
	}

	return Result{
		Message: strings.Join([]string{"HasSuffix(", m.suffix, ") Prefix is not present. Got: ", txt}, ""),
	}, nil
}

func (m *hasSuffixMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasSuffix", m.suffix)
}

// HasSuffix passes when matcher argument ends with the given suffix.
func HasSuffix(suffix string) Matcher {
	return &hasSuffixMatcher{suffix: suffix}
}
