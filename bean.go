package vlatte

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/caffeine-projects/vlatte/internal/encutil"
)

var _ Bean = (*GreenBean)(nil)

type Bean interface {
	GetID() string
	GetName() string
	GetPriority() int
	GetSource() string
	Inc()
	Dec()
	Hits() int64
	Called() bool
	Enabled() bool
	Enable()
	Disable()
	String() string
	Prepare()

	PostActions() PostActions
	Callbacks() []Callback
	Flow() []Step
}

type IDGen func() string

func UseUUID() IDGen {
	return func() string {
		return uuid.New().String()
	}
}

// Roaster describes a Mock builder.
type Roaster[B Bean, TMockApp MockApp[B]] interface {
	Roast(app TMockApp) (B, error)
}

type Canister[T Bean] interface {
	EligibleStream(ctx context.Context, done <-chan struct{}) (<-chan T, error)
	Eligible(ctx context.Context) ([]T, error)

	Save(ctx context.Context, bean ...T) error

	Get(ctx context.Context, id string) (T, error)
	GetAll(ctx context.Context) ([]T, error)
	GetByIDs(ctx context.Context, ids ...string) ([]T, error)

	Del(ctx context.Context, ids ...string) error
	DelAll(ctx context.Context) error
}

// GreenBean holds metadata and expectations to be matched against HTTP requests in order to serve mocked responses.
// This is the core entity of this project, and most features work based on it.
type GreenBean struct {
	mu      sync.RWMutex
	hits    atomic.Int64
	enabled bool

	// ID is the unique identifier of a Mock
	ID string

	// Name describes the mock. Useful for debugging.
	Name string

	// Priority sets the priority of a Mock.
	Priority int

	// Source describes the source of the mock. E.g.: if it was built from a file,
	// it will contain the filename.
	Source string

	// Delay sets the duration to delay serving the mocked response.
	Delay Delay

	PostActionList PostActions

	CallbackList []Callback

	Steps []Step
}

// NewMock returns a new Mock with default values set.
func NewMock(idgen IDGen) *GreenBean {
	return &GreenBean{
		ID:             idgen(),
		PostActionList: make(PostActions, 0),
		CallbackList:   make([]Callback, 0),
		Steps:          make([]Step, 0),

		enabled: true,
	}
}

func (m *GreenBean) GetID() string {
	return m.ID
}

func (m *GreenBean) GetName() string {
	return m.Name
}

func (m *GreenBean) GetPriority() int {
	return m.Priority
}

func (m *GreenBean) GetSource() string {
	return m.Source
}

func (m *GreenBean) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Inc increment one Mock call.
func (m *GreenBean) Inc() {
	m.hits.Add(1)
}

// Dec reduce one Mock call.
func (m *GreenBean) Dec() {
	m.hits.Add(-1)
}

// Hits returns the amount of time this Mock was matched to a request and served.
func (m *GreenBean) Hits() int64 {
	return m.hits.Load()
}

// Called checks if the Mock was called at least once.
func (m *GreenBean) Called() bool {
	return m.hits.Load() > 0
}

// Enable enables the Mock.
// The Mock will be eligible to be matched.
func (m *GreenBean) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
}

// Disable disables the Mock.
// The Mock will not be eligible to be matched.
func (m *GreenBean) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = false
}

func (m *GreenBean) Switch(state bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = state
}

func (m *GreenBean) Prepare() {
}

func (m *GreenBean) String() string {
	return ""
}

func (m *GreenBean) PostActions() PostActions {
	return m.PostActionList
}

func (m *GreenBean) Callbacks() []Callback {
	return m.CallbackList
}

func (m *GreenBean) Flow() []Step {
	return m.Steps
}

type Results struct {
	Buf []string
}

func (d *Results) Append(v string) {
	d.Buf = append(d.Buf, v)
}

func (d *Results) AppendList(sep string, v ...string) {
	d.Buf = append(d.Buf, encutil.Join(sep, v...))
}

func (d *Results) Len() int {
	return len(d.Buf)
}

func (d *Results) String() string {
	return encutil.Join("\n", d.Buf...)
}
