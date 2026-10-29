package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// hasPrefixMatcher passes if the value starts with the given prefix.
type hasPrefixMatcher struct {
	Prefix string
}

// Name implements vlatte.Matcher.
func (m *hasPrefixMatcher) Name() string { return "HasPrefix" }

// Match implements vlatte.Matcher.
func (m *hasPrefixMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := value.Text(v)
	if !ok {
		return mismatch("HasPrefix() Value is not text")
	}
	if strings.HasPrefix(txt, m.Prefix) {
		return success()
	}

	return mismatch(strings.Join([]string{"HasPrefix(", m.Prefix, ") Prefix is not present. Got: ", txt}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *hasPrefixMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("hasPrefix", m.Prefix)
}
