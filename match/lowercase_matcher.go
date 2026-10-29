package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// lowerCaseMatcher lowercases the value and matches with the inner matcher.
type lowerCaseMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *lowerCaseMatcher) Name() string { return "LowerCase" }

// Match implements vlatte.Matcher.
func (m *lowerCaseMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := value.Text(v)
	if !ok {
		return mismatch("Lower() Value is not text")
	}

	result, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), strings.ToLower(txt)))
	if err != nil {
		return failure(fmt.Errorf("lower: %w", err))
	}

	if result {
		return success()
	}

	return mismatch("Lower() value did not match")
}

// Describe implements vlatte.SelfDescriptive.
func (m *lowerCaseMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("lowercase", describe(m.Matcher))
}
