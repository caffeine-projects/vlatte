package mockgrpc

import (
	"sync"
	"sync/atomic"
	"uuid"

	"github.com/caffeine-projects/vlatte"
)

type interceptors struct {
	app *Server
}

type Mock struct {
	mu sync.RWMutex

	id       string
	name     string
	priority int
	hits     atomic.Int64
	enabled  bool

	Reply any

	unaryExpectations  []*vlatte.Eval[*UnaryValueSelectorIn]
	streamExpectations []*vlatte.Eval[*StreamValueSelectorIn]
}

func newMock() *Mock {
	return &Mock{
		id:      uuid.New().String(),
		enabled: true,
	}
}

func (m *Mock) ID() string {
	return m.id
}

func (m *Mock) Name() string {
	return m.name
}

func (m *Mock) Priority() int {
	return m.priority
}

func (m *Mock) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Inc increment one Mock call.
func (m *Mock) Inc() {
	m.hits.Add(1)
}

// Hits returns the amount of time this Mock was matched to a request and served.
func (m *Mock) Hits() int64 {
	return m.hits.Load()
}

// Called checks if the Mock was called at least once.
func (m *Mock) Called() bool {
	return m.hits.Load() > 0
}

// Enable enables the Mock.
// The Mock will be eligible to be matched.
func (m *Mock) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
}

// Disable disables the Mock.
// The Mock will not be eligible to be matched.
func (m *Mock) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = false
}

// Events returns no events: the gRPC server does not run on-complete events.
func (m *Mock) Events() vlatte.Events {
	return vlatte.Events{}
}

func (m *Mock) String() string {
	return ""
}

func (m *Mock) getRef() string {
	if len(m.Name()) == 0 {
		return m.ID()
	}

	return m.Name()
}
