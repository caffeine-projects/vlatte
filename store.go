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
	beans []B
	mu    sync.RWMutex
}

func NewStore[B Mock]() Store[B] {
	return &MemoryStore[B]{beans: make([]B, 0)}
}

func (s *MemoryStore[B]) List(ctx context.Context) (iter.Seq[B], error) {
	s.mu.RLock()
	beans := make([]B, len(s.beans))
	copy(beans, s.beans)
	s.mu.RUnlock()

	return func(yield func(B) bool) {
		for _, bean := range beans {
			if bean.Enabled() {
				if !yield(bean) {
					break
				}
			}
		}
	}, nil
}

func (s *MemoryStore[B]) Store(_ context.Context, beans ...B) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.beans = append(s.beans, beans...)

	sort.SliceStable(s.beans, func(a, b int) bool {
		return s.beans[a].Priority() < s.beans[b].Priority()
	})

	return nil
}

func (s *MemoryStore[B]) Pick(_ context.Context, id string) (B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, bean := range s.beans {
		if bean.ID() == id {
			return bean, nil
		}
	}

	var result B
	return result, ErrNotFound
}

func (s *MemoryStore[B]) PickAll(_ context.Context) ([]B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]B, len(s.beans))
	copy(ret, s.beans)

	return ret, nil
}

func (s *MemoryStore[B]) Delete(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	newBeans := s.beans[:0]
	for _, bean := range s.beans {
		if _, found := idSet[bean.ID()]; !found {
			newBeans = append(newBeans, bean)
		}
	}

	s.beans = newBeans

	return nil
}

func (s *MemoryStore[B]) Clear(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.beans = make([]B, 0)

	return nil
}
