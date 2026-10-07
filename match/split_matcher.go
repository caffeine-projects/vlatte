package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// splitMatcher splits the value by separator and matches with the inner matcher.
type splitMatcher struct {
	Separator string
	Matcher   vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *splitMatcher) Name() string { return "Split" }

// Match implements vlatte.Matcher.
func (m *splitMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	txt, ok := value.Text(v)
	if !ok {
		return mismatch("Split() Value is not text")
	}

	result, err := m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), strings.Split(txt, m.Separator)))
	if err != nil {
		return failure(fmt.Errorf("split: %w", err))
	}

	if result {
		return success()
	}

	return mismatch(strings.Join([]string{"Split(", m.Separator, ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *splitMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("split", m.Separator, describe(m.Matcher))
}
