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

func (m *globMatcher) Match(v any) (bool, vlatte.Reason, error) {
	text, ok := v.(string)
	if !ok {
		return false, ReasonError, fmt.Errorf("glob: it only works with string types. got %T", v)
	}

	if glob.Glob(m.pattern, text) {
		return success()
	}

	return mismatch(strings.Join([]string{"Glob(", m.pattern, ") ", text}, ""))
}

func (m *globMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("glob", m.pattern)
}

func GlobMatch(pattern string) vlatte.Filter {
	return &globMatcher{pattern}
}
