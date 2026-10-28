package matchrepeat

import (
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// Repeat returns a matcher that passes until the max number of matches is reached.
func Repeat(times int64) *repeatMatcher {
	return &repeatMatcher{Max: times}
}

// repeatMatcher passes until the max number of matches is reached (stateful).
type repeatMatcher struct {
	Max  int64
	hits atomic.Int64
}

// Name implements vlatte.Matcher.
func (m *repeatMatcher) Name() string { return "Repeat" }

// Match implements vlatte.Matcher.
func (m *repeatMatcher) Match(_ vlatte.MatcherArg) (bool, error) {
	return m.hits.Load() < m.Max, nil
}

// SetState implements vlatte.Stateful.
func (m *repeatMatcher) SetState() error {
	m.hits.Add(1)
	return nil
}

// Describe implements descriptor.SelfDescriptive.
func (m *repeatMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("repeat", m.Max)
}
