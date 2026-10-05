package vlatte_test

import (
	"strconv"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Mock = (*fakeMock)(nil)

var fakeMockSeq atomic.Int64

// fakeMock is a protocol-free vlatte.Mock, so core tests don't depend on a protocol package.
type fakeMock struct {
	id      string
	hits    atomic.Int64
	enabled atomic.Bool
	events  vlatte.Events
}

func newFakeMock() *fakeMock {
	m := &fakeMock{id: "mock-" + strconv.FormatInt(fakeMockSeq.Add(1), 10)}
	m.enabled.Store(true)

	return m
}

func (m *fakeMock) ID() string            { return m.id }
func (m *fakeMock) Name() string          { return m.id }
func (m *fakeMock) Priority() int         { return 0 }
func (m *fakeMock) Hits() int64           { return m.hits.Load() }
func (m *fakeMock) Called() bool          { return m.hits.Load() > 0 }
func (m *fakeMock) Enabled() bool         { return m.enabled.Load() }
func (m *fakeMock) Enable()               { m.enabled.Store(true) }
func (m *fakeMock) Disable()              { m.enabled.Store(false) }
func (m *fakeMock) Events() vlatte.Events { return m.events }
func (m *fakeMock) String() string        { return m.id }

// hit records a served request, as a protocol server does after a mock matches.
func (m *fakeMock) hit() { m.hits.Add(1) }
