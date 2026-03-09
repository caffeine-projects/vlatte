package mfeat

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type repeatMatcher struct {
	max  int64
	hits int64
}

func (m *repeatMatcher) Match(_ any) (bool, vlatte.Reason, error) {
	if atomic.LoadInt64(&m.hits) < m.max {
		return true, vlatte.Reason(""), nil
	}

	return false, vlatte.Reason(strings.Join([]string{
		"Repeat(",
		strconv.FormatInt(m.max, 10),
		") Reached the max matched requests for this mock",
	}, "")), nil
}

func (m *repeatMatcher) SetState() error {
	atomic.AddInt64(&m.hits, 1)

	return nil
}

func (m *repeatMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("repeat", m.max)
}

func Repeat(times int64) vlatte.Filter {
	return &repeatMatcher{max: times}
}
