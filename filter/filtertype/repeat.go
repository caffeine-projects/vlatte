package filtertype

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// RepeatMatcher passes until the max number of matches is reached (stateful).
type RepeatMatcher struct {
	Max  int64
	hits int64
}

// Match implements vlatte.Filter.
func (m *RepeatMatcher) Match(_ any) (bool, vlatte.Reason, error) {
	if atomic.LoadInt64(&m.hits) < m.Max {
		return true, vlatte.Reason(""), nil
	}

	return false, vlatte.Reason(strings.Join([]string{
		"Repeat(",
		strconv.FormatInt(m.Max, 10),
		") Reached the max matched requests for this mock",
	}, "")), nil
}

// SetState implements vlatte.Stateful.
func (m *RepeatMatcher) SetState() error {
	atomic.AddInt64(&m.hits, 1)
	return nil
}

// Describe implements descriptor.SelfDescriptive.
func (m *RepeatMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("repeat", m.Max)
}
