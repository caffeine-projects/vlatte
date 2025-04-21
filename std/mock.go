package std

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte/internal/encutil"
	"github.com/google/uuid"
)

var _ Mock = (*M)(nil)

type Mock interface {
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
}

type IDGen func() string

func UseUUID() IDGen {
	return func() string {
		return uuid.New().String()
	}
}

// Builder describes a Mock builder.
type Builder[TMock Mock, TMockApp MockApp[TMock]] interface {
	Build(app TMockApp) (TMock, error)
}

type Store[T Mock] interface {
	EligibleStream(ctx context.Context, done <-chan struct{}) (<-chan T, error)
	Eligible(ctx context.Context) ([]T, error)

	Save(ctx context.Context, mock ...T) error

	Get(ctx context.Context, id string) (T, error)
	GetAll(ctx context.Context) ([]T, error)
	GetByIDs(ctx context.Context, ids ...string) ([]T, error)

	Del(ctx context.Context, ids ...string) error
	DelAll(ctx context.Context) error
}

// M holds metadata and expectations to be matched against HTTP requests in order to serve mocked responses.
// This is the core entity of this project, and most features work based on it.
type M struct {
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
}

// NewMock returns a new Mock with default values set.
func NewMock(idgen IDGen) *M {
	mock := &M{
		ID:             idgen(),
		PostActionList: make(PostActions, 0),
		CallbackList:   make([]Callback, 0),

		enabled: true,
	}

	return mock
}

func (m *M) GetID() string {
	return m.ID
}

func (m *M) GetName() string {
	return m.Name
}

func (m *M) GetPriority() int {
	return m.Priority
}

func (m *M) GetSource() string {
	return m.Source
}

func (m *M) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// Inc increment one Mock call.
func (m *M) Inc() {
	m.hits.Add(1)
}

// Dec reduce one Mock call.
func (m *M) Dec() {
	m.hits.Add(-1)
}

// Hits returns the amount of time this Mock was matched to a request and served.
func (m *M) Hits() int64 {
	return m.hits.Load()
}

// Called checks if the Mock was called at least once.
func (m *M) Called() bool {
	return m.hits.Load() > 0
}

// Enable enables the Mock.
// The Mock will be eligible to be matched.
func (m *M) Enable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = true
}

// Disable disables the Mock.
// The Mock will not be eligible to be matched.
func (m *M) Disable() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = false
}

func (m *M) Switch(state bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = state
}

func (m *M) Prepare() {
}

func (m *M) String() string {
	return ""
}

func (m *M) PostActions() PostActions {
	return m.PostActionList
}

func (m *M) Callbacks() []Callback {
	return m.CallbackList
}

// Build allows users to use the Mock itself as a HTTPMockBuilder.
func (m *M) Build() (*M, error) {
	return m, nil
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
