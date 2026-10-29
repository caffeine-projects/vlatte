package match

import (
	"strings"

	"github.com/ryanuber/go-glob"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
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
	text, ok := value.Text(v)
	if !ok {
		return mismatch("Glob() Value is not text")
	}

	if glob.Glob(m.Pattern, text) {
		return success()
	}

	return mismatch(strings.Join([]string{"Glob(", m.Pattern, ") ", text}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *globMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("glob", m.Pattern)
}
