package vlatte_test

import (
	"context"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Mock = (*fakeMock)(nil)

var fakeMockSeq atomic.Int64

// fakeMock is a protocol-free vlatte.Mock, so core tests don't depend on a protocol package.
type fakeMock struct {
	id       string
	priority int
	hits     atomic.Int64
	enabled  atomic.Bool
	events   vlatte.Events
}

func newFakeMock() *fakeMock {
	return newFakeMockWith("mock-"+strconv.FormatInt(fakeMockSeq.Add(1), 10), 0)
}

// newFakeMockWith returns an enabled fake mock with the given ID and priority.
func newFakeMockWith(id string, priority int) *fakeMock {
	m := &fakeMock{id: id, priority: priority}
	m.enabled.Store(true)

	return m
}

func (m *fakeMock) ID() string            { return m.id }
func (m *fakeMock) Name() string          { return m.id }
func (m *fakeMock) Priority() int         { return m.priority }
func (m *fakeMock) Hits() int64           { return m.hits.Load() }
func (m *fakeMock) Called() bool          { return m.hits.Load() > 0 }
func (m *fakeMock) Enabled() bool         { return m.enabled.Load() }
func (m *fakeMock) Enable()               { m.enabled.Store(true) }
func (m *fakeMock) Disable()              { m.enabled.Store(false) }
func (m *fakeMock) Events() vlatte.Events { return m.events }
func (m *fakeMock) String() string        { return m.id }

// hit records a served request, as a protocol server does after a mock matches.
func (m *fakeMock) hit() { m.hits.Add(1) }

// brokenStore is a memory store whose Pick, Delete or Replace fails, for the error paths of scopes.
type brokenStore struct {
	*vlatte.MemoryStore[*fakeMock]

	pickErr    error
	deleteErr  error
	replaceErr error
}

func newBrokenStore(pickErr, deleteErr error) *brokenStore {
	return &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), pickErr: pickErr, deleteErr: deleteErr}
}

func (s *brokenStore) Pick(ctx context.Context, id string) (*fakeMock, error) {
	if s.pickErr != nil {
		return nil, s.pickErr
	}

	return s.MemoryStore.Pick(ctx, id)
}

func (s *brokenStore) Delete(ctx context.Context, ids ...string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}

	return s.MemoryStore.Delete(ctx, ids...)
}

func (s *brokenStore) Replace(ctx context.Context, old []*fakeMock, mocks ...*fakeMock) error {
	if s.replaceErr != nil {
		return s.replaceErr
	}

	return s.MemoryStore.Replace(ctx, old, mocks...)
}

// sameMocks reports whether got holds exactly the instances of want, in any order: mocks are told apart by identity.
func sameMocks(want, got []*fakeMock) bool {
	if len(want) != len(got) {
		return false
	}

	for _, w := range want {
		if !slices.Contains(got, w) {
			return false
		}
	}

	return true
}
