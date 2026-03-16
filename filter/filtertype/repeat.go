package filtertype

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Repeat passes until the max number of matches is reached (stateful).
type Repeat struct {
	Max  int64
	hits int64
}

// Name implements vlatte.Filter.
func (m *Repeat) Name() string { return "Repeat" }

// Match implements vlatte.Filter.
func (m *Repeat) Match(_ vlatte.FilterArg) (bool, error) {
	if atomic.LoadInt64(&m.hits) < m.Max {
		return true, nil
	}

	return Mismatch(strings.Join([]string{
		"Repeat(",
		strconv.FormatInt(m.Max, 10),
		") Reached the max matched requests for this mock",
	}, ""))
}

// SetState implements vlatte.Stateful.
func (m *Repeat) SetState() error {
	atomic.AddInt64(&m.hits, 1)
	return nil
}

// Describe implements descriptor.SelfDescriptive.
func (m *Repeat) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("repeat", m.Max)
}
