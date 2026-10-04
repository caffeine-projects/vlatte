package vlatte

import (
	"context"
	"errors"
	"iter"
	"sort"
	"sync"
)

// ErrNotFound is returned by [Store.Pick] when no mock with the given ID exists.
var ErrNotFound = errors.New("store: mock not found")

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

func (s *MemoryStore[B]) Store(_ context.Context, mocks ...B) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mocks = append(s.mocks, mocks...)

	sort.SliceStable(s.mocks, func(a, b int) bool {
		return s.mocks[a].Priority() < s.mocks[b].Priority()
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
	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	kept := s.mocks[:0]
	for _, mock := range s.mocks {
		if _, found := idSet[mock.ID()]; !found {
			kept = append(kept, mock)
		}
	}

	s.mocks = kept

	return nil
}

func (s *MemoryStore[B]) Clear(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mocks = make([]B, 0)

	return nil
}
