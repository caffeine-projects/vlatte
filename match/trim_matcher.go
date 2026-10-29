package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// trimMatcher trims spaces and matches with the inner matcher.
type trimMatcher struct {
	Matcher vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *trimMatcher) Name() string { return "Trim" }

// Match implements vlatte.Matcher.
func (m *trimMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := value.Text(v)
	if !ok {
		return mismatch("Trim() Value is not text")
	}

	result, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), strings.TrimSpace(txt)))
	if err != nil {
		return failure(fmt.Errorf("trim: %w", err))
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Trim(", txt, ")"}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *trimMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("trim", describe(m.Matcher))
}
