package vlatte

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"slices"
	"sync"
)

var (
	// ErrNotFound is returned by [Store.Pick] when no mock with the given ID exists.
	ErrNotFound = errors.New("store: mock not found")

	// ErrDuplicateID is returned by [Store.Store] when the ID of a mock is already stored, or repeated in the call.
	ErrDuplicateID = errors.New("store: mock id already in use")
)

// MemoryStore is a [Store] that keeps its mocks in memory.
type MemoryStore[B Mock] struct {
	mocks []B
	mu    sync.RWMutex
}

// NewStore creates an empty [MemoryStore].
func NewStore[B Mock]() Store[B] {
	return &MemoryStore[B]{mocks: make([]B, 0)}
}

// List returns the enabled mocks, in the order they match, as they were when List was called. It never fails.
func (s *MemoryStore[B]) List(ctx context.Context) (iter.Seq[B], error) {
	s.mu.RLock()
	mocks := make([]B, len(s.mocks))
	copy(mocks, s.mocks)
	s.mu.RUnlock()

	return func(yield func(B) bool) {
		for _, mock := range mocks {
			if mock.Enabled() {
				if !yield(mock) {
					break
				}
			}
		}
	}, nil
}

// Store adds mocks, ordered by priority and then by when they were stored.
// It stores none of them when an ID is already stored or repeated in the call.
func (s *MemoryStore[B]) Store(_ context.Context, mocks ...B) error {
	return s.swap(func(B) bool { return false }, mocks)
}

// Replace removes the mocks of old it holds, the very instances, and stores mocks, in one step. A mock of old that it
// stores again keeps its place, so it matches in the same order.
// It changes nothing when an ID of mocks is held by a mock it keeps, or repeated in the call.
func (s *MemoryStore[B]) Replace(_ context.Context, old []B, mocks ...B) error {
	gone := make(map[any]struct{}, len(old))
	for _, mock := range old {
		gone[mock] = struct{}{}
	}

	return s.swap(func(mock B) bool {
		_, ok := gone[mock]
		return ok
	}, mocks)
}

// swap removes the mocks drop reports and adds mocks, in one step, ordered by priority and then by when they were
// stored: a mock it removes and adds back keeps its place. It changes nothing when an ID of mocks is held by a mock it
// keeps, or repeated in the call. Once the store is unlocked, it closes the mocks it removed, unless it added them
// back.
func (s *MemoryStore[B]) swap(drop func(B) bool, mocks []B) error {
	gone, err := s.exchange(drop, mocks)
	if err != nil {
		return err
	}

	closeMocks(gone)

	return nil
}

// exchange is swap under the lock. It returns the mocks it removed and didn't add back.
func (s *MemoryStore[B]) exchange(drop func(B) bool, mocks []B) ([]B, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make(map[string]struct{}, len(s.mocks)+len(mocks))
	for _, mock := range s.mocks {
		if !drop(mock) {
			ids[mock.ID()] = struct{}{}
		}
	}

	for _, mock := range mocks {
		if _, taken := ids[mock.ID()]; taken {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateID, mock.ID())
		}

		ids[mock.ID()] = struct{}{}
	}

	added := make(map[any]struct{}, len(mocks))
	for _, mock := range mocks {
		added[mock] = struct{}{}
	}

	// Only now: deleting in place changes the stored mocks, which a refused call must leave alone. A mock added back
	// stays where it is.
	var gone []B

	kept := make(map[any]struct{})
	s.mocks = slices.DeleteFunc(s.mocks, func(mock B) bool {
		if !drop(mock) {
			return false
		}

		if _, back := added[mock]; back {
			kept[mock] = struct{}{}
			return false
		}

		gone = append(gone, mock)

		return true
	})

	for _, mock := range mocks {
		if _, ok := kept[mock]; !ok {
			s.mocks = append(s.mocks, mock)
		}
	}

	slices.SortStableFunc(s.mocks, func(a, b B) int {
		return cmp.Compare(a.Priority(), b.Priority())
	})

	return gone, nil
}

// Pick returns the mock with the given ID, enabled or not. It fails with [ErrNotFound] when no mock has it.
func (s *MemoryStore[B]) Pick(_ context.Context, id string) (B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, mock := range s.mocks {
		if mock.ID() == id {
			return mock, nil
		}
	}

	var result B
	return result, ErrNotFound
}

// PickAll returns every mock, enabled or not, in the order they match. It never fails.
func (s *MemoryStore[B]) PickAll(_ context.Context) ([]B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]B, len(s.mocks))
	copy(ret, s.mocks)

	return ret, nil
}

// Delete removes the mocks with the given IDs, skipping those it doesn't hold, and closes the removed mocks that
// implement [io.Closer] once the store is unlocked. It never fails.
func (s *MemoryStore[B]) Delete(_ context.Context, ids ...string) error {
	gone := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		gone[id] = struct{}{}
	}

	return s.swap(func(mock B) bool {
		_, ok := gone[mock.ID()]
		return ok
	}, nil)
}

// Clear removes every mock, and closes them once the store is unlocked.
func (s *MemoryStore[B]) Clear(_ context.Context) error {
	s.mu.Lock()
	gone := s.mocks
	s.mocks = make([]B, 0)
	s.mu.Unlock()

	closeMocks(gone)

	return nil
}

// Health returns nil: a memory store is always ready.
func (s *MemoryStore[B]) Health(context.Context) error {
	return nil
}

// closeMocks closes the mocks that implement io.Closer. A mock that failed to close has left already: there is no one
// to report the error to, so closing is the mock's to get right.
func closeMocks[B Mock](mocks []B) {
	for _, mock := range mocks {
		if c, ok := any(mock).(io.Closer); ok {
			_ = c.Close()
		}
	}
}
