package xgrpc

import (
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
)

type Interceptors struct {
	app *GRPCMockApp
}

type GRPCMockBuilder interface {
	Grind(*GRPCMockApp) (*GRPCMock, error)
}

type GRPCMock struct {
	mu sync.RWMutex

	id       string
	name     string
	priority int
	origin   string
	hits     atomic.Int64
	enabled  bool

	Reply any

	PostActionList vlatte.PostActions

	CallbackList []vlatte.Callback

	Steps []vlatte.Step

	after              []filter.OnMockSent
	unaryExpectations  []*vlatte.Eval[*UnaryValueSelectorIn]
	streamExpectations []*vlatte.Eval[*StreamValueSelectorIn]
}

func newMock() *GRPCMock {
	return &GRPCMock{
		id:      uuid.New().String(),
		after:   make([]filter.OnMockSent, 0),
		enabled: true,

		PostActionList: make(vlatte.PostActions, 0),
		CallbackList:   make([]vlatte.Callback, 0),
		Steps:          make([]vlatte.Step, 0),
	}
}

func (m *GRPCMock) ID() string {
	return m.id
}

func (m *GRPCMock) Name() string {
	return m.name
}

func (m *GRPCMock) Priority() int {
	return m.priority
}

func (m *GRPCMock) Origin() string {
	return m.origin
}

func (m *GRPCMock) Roasted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Inc increment one Mock call.
func (m *GRPCMock) Inc() {
	m.hits.Add(1)
}

// Dec reduce one Mock call.
func (m *GRPCMock) Dec() {
	m.hits.Add(-1)
}

// Hits returns the amount of time this Mock was matched to a request and served.
func (m *GRPCMock) Hits() int64 {
	return m.hits.Load()
}

// Called checks if the Mock was called at least once.
func (m *GRPCMock) Called() bool {
	return m.hits.Load() > 0
}

// Enable enables the Mock.
// The Mock will be eligible to be matched.
func (m *GRPCMock) Roast() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
}

// Disable disables the Mock.
// The Mock will not be eligible to be matched.
func (m *GRPCMock) Raw() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = false
}

func (m *GRPCMock) PostActions() vlatte.PostActions {
	return m.PostActionList
}

func (m *GRPCMock) Callbacks() []vlatte.Callback {
	return m.CallbackList
}

func (m *GRPCMock) Flow() []vlatte.Step {
	return m.Steps
}

func (m *GRPCMock) Prepare() {
}

func (m *GRPCMock) String() string {
	return ""
}

func (m *GRPCMock) getRef() string {
	if len(m.Name()) == 0 {
		return m.ID()
	}

	return m.Name()
}
