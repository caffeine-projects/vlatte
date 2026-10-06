package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// upperCaseMatcher uppercases the value and matches with the inner matcher.
type upperCaseMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *upperCaseMatcher) Name() string { return "UpperCase" }

// Match implements vlatte.Matcher.
func (m *upperCaseMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := value.Text(v)
	if !ok {
		return mismatch("Upper() Value is not text")
	}

	result, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), strings.ToUpper(txt)))
	if err != nil {
		return failure(fmt.Errorf("upper: %w", err))
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Upper(", txt, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *upperCaseMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("uppercase", describe(m.Matcher))
}
