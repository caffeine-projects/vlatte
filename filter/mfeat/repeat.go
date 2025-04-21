package mfeat

import (
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/std/descriptor"
)

type repeatMatcher struct {
	max  int64
	hits int64
}

func (m *repeatMatcher) Match(_ any) (filter.Result, error) {
	if atomic.LoadInt64(&m.hits) < m.max {
		return filter.Result{Pass: true}, nil
	}

	return filter.Result{
		Message: strings.Join([]string{"Repeat(", strconv.FormatInt(m.max, 10), ") Reached the max matched requests for this mock"}, ""),
	}, nil
}

func (m *repeatMatcher) OnMockSent() error {
	atomic.AddInt64(&m.hits, 1)

	return nil
}

func (m *repeatMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("repeat", m.max)
}

func Repeat(times int64) filter.Filter {
	return &repeatMatcher{max: times}
}
