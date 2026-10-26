package mockgrpc

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Mock = (*Mock)(nil)

type interceptors struct {
	app *Server
}

// Mock is a mock of a gRPC method, which a [Builder] builds.
type Mock struct {
	mu sync.RWMutex

	id       string
	name     string
	priority int
	hits     atomic.Int64
	enabled  bool
	origin   string

	method   string   // the name the builder was given
	methods  []string // the full methods method names
	callType callType

	expectations []*vlatte.Eval[*Call]
	delay        vlatte.Delay
	events       vlatte.EventConfigs
	reply        Reply // what the builder replies with, for its description
	replier      Replier
	doc          map[string]any // the mock document the mock was read from, nil for a mock built in Go
	described    mockDescription
}

// ID returns the ID of the mock.
func (m *Mock) ID() string {
	return m.id
}

// Name returns the name of the mock, or "" when it has none.
func (m *Mock) Name() string {
	return m.name
}

// Priority returns the priority of the mock: mocks with a lower one match first.
func (m *Mock) Priority() int {
	return m.priority
}

// Origin returns where the mock comes from, such as the mock file that describes it, or "" for a mock built in Go.
func (m *Mock) Origin() string {
	return m.origin
}

// Methods returns the full methods the mock serves: those its name names.
func (m *Mock) Methods() []string {
	return slices.Clip(m.methods)
}

// Enabled reports whether the mock may match calls.
func (m *Mock) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.enabled
}

// Inc counts one call of the mock.
func (m *Mock) Inc() {
	m.hits.Add(1)
}

// Hits returns how many calls the mock answered, with messages or with a status, and is answering.
func (m *Mock) Hits() int64 {
	return m.hits.Load()
}

// Called reports whether the mock answered a call.
func (m *Mock) Called() bool {
	return m.hits.Load() > 0
}

// Enable lets the mock match calls.
func (m *Mock) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enabled = true
}

// Disable stops the mock from matching calls.
func (m *Mock) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enabled = false
}

// Events returns the events the mock runs once it replied, its callbacks included.
func (m *Mock) Events() vlatte.Events {
	return vlatte.Events{OnComplete: m.events}
}

// String names the mock as "Mock [id] name", as assertion messages do.
func (m *Mock) String() string {
	return strings.TrimSpace("Mock [" + m.ID() + "] " + m.Name())
}

// wait waits for the mock's delay, if it has one.
func (m *Mock) wait() time.Duration {
	if m.delay == nil {
		return 0
	}

	return m.delay()
}
