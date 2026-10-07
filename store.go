package vlatte

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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

type MemoryStore[B Mock] struct {
	mocks []B
	mu    sync.RWMutex
}

func NewStore[B Mock]() Store[B] {
	return &MemoryStore[B]{mocks: make([]B, 0)}
}

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

// Replace removes the mocks of old it holds, the very instances, and stores mocks, in one step.
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
// stored. It changes nothing when an ID of mocks is held by a mock it keeps, or repeated in the call.
func (s *MemoryStore[B]) swap(drop func(B) bool, mocks []B) error {
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
			return fmt.Errorf("%w: %q", ErrDuplicateID, mock.ID())
		}

		ids[mock.ID()] = struct{}{}
	}

	// Only now: deleting in place changes the stored mocks, which a refused call must leave alone.
	s.mocks = append(slices.DeleteFunc(s.mocks, drop), mocks...)

	slices.SortStableFunc(s.mocks, func(a, b B) int {
		return cmp.Compare(a.Priority(), b.Priority())
	})

	return nil
}

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

func (s *MemoryStore[B]) PickAll(_ context.Context) ([]B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]B, len(s.mocks))
	copy(ret, s.mocks)

	return ret, nil
}

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

func (s *MemoryStore[B]) Clear(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mocks = make([]B, 0)

	return nil
}
