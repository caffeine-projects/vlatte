package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// equalIgnoreCaseMatcher passes when the string value equals expected ignoring case.
type equalIgnoreCaseMatcher struct {
	Expected string
}

// Name implements vlatte.Matcher.
func (m *equalIgnoreCaseMatcher) Name() string { return "EqualIgnoreCase" }

// Match implements vlatte.Matcher.
func (m *equalIgnoreCaseMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt := ""
	if v != nil {
		var ok bool
		txt, ok = v.(string)
		if !ok {
			return failure(fmt.Errorf("eqi: value must be a string. got: %T", v))
		}
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
