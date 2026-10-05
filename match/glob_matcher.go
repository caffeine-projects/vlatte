package match

import (
	"fmt"
	"strings"

	"github.com/ryanuber/go-glob"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// globMatcher passes when the value matches the glob pattern.
type globMatcher struct {
	Pattern string
}

// Name implements vlatte.Matcher.
func (m *globMatcher) Name() string { return "Glob" }

// Match implements vlatte.Matcher.
func (m *globMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	text, ok := v.(string)
	if !ok {
		return failure(fmt.Errorf("glob: it only works with string types. got %T", v))
	}

	if glob.Glob(m.Pattern, text) {
		return success()
	}

	return mismatch(strings.Join([]string{"Glob(", m.Pattern, ") ", text}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *globMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("glob", m.Pattern)
}
