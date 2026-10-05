package match

import (
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// emptyMatcher passes when the value is empty or has zero length.
type emptyMatcher struct{}

// Name implements vlatte.Matcher.
func (m *emptyMatcher) Name() string { return "Empty" }

// Match implements vlatte.Matcher.
func (m *emptyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
	if v == nil {
		return success()
	}

	zeroLen := &lenMatcher{Length: 0}
	ok, err := zeroLen.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("empty: %w", err))
	}

	if ok {
		return success()
	}

	return mismatch(strings.Join([]string{"Empty(", anyconv.Stringify(v), ")"}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *emptyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("empty")
}
