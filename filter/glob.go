package filter

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte/std/descriptor"
	"github.com/ryanuber/go-glob"
)

type globMatcher struct {
	pattern string
}

func (m *globMatcher) Match(v any) (Result, error) {
	text, ok := v.(string)
	if !ok {
		return Result{}, fmt.Errorf("glob: it only works with string types. got %T", v)
	}

	if glob.Glob(m.pattern, text) {
		return Result{Pass: true}, nil
	}

	return Result{Message: strings.Join([]string{"Glob(", m.pattern, ") ", text}, "")}, nil
}

func (m *globMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("glob", m.pattern)
}

func GlobMatch(pattern string) Filter {
	return &globMatcher{pattern}
}
