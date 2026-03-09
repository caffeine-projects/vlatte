package filter

import (
	"fmt"
	"strings"

	"github.com/ryanuber/go-glob"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type globMatcher struct {
	pattern string
}

func (m *globMatcher) Match(v any) (vlatte.Result, error) {
	text, ok := v.(string)
	if !ok {
		return vlatte.Result{}, fmt.Errorf("glob: it only works with string types. got %T", v)
	}

	if glob.Glob(m.pattern, text) {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{Message: strings.Join([]string{"Glob(", m.pattern, ") ", text}, "")}, nil
}

func (m *globMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("glob", m.pattern)
}

func GlobMatch(pattern string) vlatte.Filter {
	return &globMatcher{pattern}
}
