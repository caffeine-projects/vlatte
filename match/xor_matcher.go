package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// xorMatcher is an exclusive OR matcher.
type xorMatcher struct {
	First  vlatte.Matcher
	Second vlatte.Matcher
}

// Name implements vlatte.Matcher.
func (m *xorMatcher) Name() string { return "Xor" }

// Match implements vlatte.Matcher.
func (m *xorMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	aOK, err := m.First.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("xor: first: %w", err))
	}

	bOK, err := m.Second.Match(arg)
	if err != nil {
		return failure(fmt.Errorf("xor: second: %w", err))
	}

	if aOK != bOK {
		return success()
	}

	return mismatch("XOR() both matched or both failed")
}

// Describe implements vlatte.SelfDescriptive.
func (m *xorMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeMultiArgs("xor", describe(m.First), describe(m.Second))
}
