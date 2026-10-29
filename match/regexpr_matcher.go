package match

import (
	"regexp"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// regExpMatcher passes when the regular expression matches the value.
type regExpMatcher struct {
	Expression string
	rg         *regexp.Regexp
}

// Name implements vlatte.Matcher.
func (m *regExpMatcher) Name() string { return "RegExp" }

// Match implements vlatte.Matcher.
func (m *regExpMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	txt, ok := value.Text(arg.Value())
	if !ok {
		return mismatch("Match() Value is not text")
	}

	if m.rg.MatchString(txt) {
		return success()
	}

	return mismatch(strings.Join([]string{"Match(", m.Expression, ") Expression did not match. Got: ", txt}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *regExpMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("regexp", m.Expression)
}
