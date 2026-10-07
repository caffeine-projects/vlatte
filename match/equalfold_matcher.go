package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// equalIgnoreCaseMatcher passes when the string value equals expected ignoring case.
type equalIgnoreCaseMatcher struct {
	Expected string
}

// Name implements vlatte.Matcher.
func (m *equalIgnoreCaseMatcher) Name() string { return "EqualIgnoreCase" }

// Match implements vlatte.Matcher.
func (m *equalIgnoreCaseMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	txt, ok := value.Text(arg.Value())
	if !ok {
		return mismatch("Eqi() Value is not text")
	}

	if strings.EqualFold(m.Expected, txt) {
		return success()
	}

	return mismatch(strings.Join([]string{"Eqi(", m.Expected, ") Got: ", txt}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalIgnoreCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("eqi", m.Expected)
}
