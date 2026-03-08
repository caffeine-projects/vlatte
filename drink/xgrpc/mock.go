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
	buzzers  *vlatte.Buzzers
	ext      any

	Reply any

	OnCompleteList []vlatte.EventConfig

	CallbackList []vlatte.Callback

	steps []vlatte.Step

	after              []filter.Stateful
	unaryExpectations  []*vlatte.Eval[*UnaryValueSelectorIn]
	streamExpectations []*vlatte.Eval[*StreamValueSelectorIn]
}

func newMock() *GRPCMock {
	return &GRPCMock{
		id:      uuid.New().String(),
		after:   make([]filter.Stateful, 0),
		enabled: true,

		OnCompleteList: make([]vlatte.EventConfig, 0),
		CallbackList:   make([]vlatte.Callback, 0),
		steps:          make([]vlatte.Step, 0),
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

func (m *GRPCMock) Ext() any {
	return m.ext
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

func (m *GRPCMock) TemplateDataProvider() vlatte.TemplateDataProvider {
	return nil
}

func (m *GRPCMock) Events() vlatte.Events {
	return vlatte.Events{OnComplete: m.OnCompleteList}
}

func (m *GRPCMock) Buzzers() *vlatte.Buzzers {
	return m.buzzers
}

func (m *GRPCMock) Callbacks() []vlatte.Callback {
	return m.CallbackList
}

func (m *GRPCMock) Steps() []vlatte.Step {
	return m.steps
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
