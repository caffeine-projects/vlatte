package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// hasSuffixMatcher passes when the value ends with the given suffix.
type hasSuffixMatcher struct {
	Suffix string
}

// Name implements vlatte.Matcher.
func (m *hasSuffixMatcher) Name() string { return "HasSuffix" }

// Match implements vlatte.Matcher.
func (m *hasSuffixMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := value.Text(v)
	if !ok {
		return mismatch("HasSuffix() Value is not text")
	}

	if strings.HasSuffix(txt, m.Suffix) {
		return success()
	}

	return mismatch(strings.Join([]string{"HasSuffix(", m.Suffix, ") Suffix is not present. Got: ", txt}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *hasSuffixMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("hasSuffix", m.Suffix)
}
