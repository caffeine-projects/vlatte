package kcore

import (
	"context"
	"sort"
	"sync"
)

var _ Store[*M] = (*InMemMockStore[*M])(nil)

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

type InMemMockStore[TMock Mock] struct {
	data []TMock
	mu   sync.RWMutex
}

// NewStore returns Mock InMemMockStore implementation.
func NewStore[TMock Mock]() *InMemMockStore[TMock] {
	return &InMemMockStore[TMock]{data: make([]TMock, 0)}
}

func (s *InMemMockStore[TMock]) EligibleStream(ctx context.Context, done <-chan struct{}) (<-chan TMock, error) {
	s.mu.RLock()
	mocks := make([]TMock, len(s.data))
	copy(mocks, s.data)
	s.mu.RUnlock()

	out := make(chan TMock, len(mocks))
	go func() {
		defer close(out)
		for _, mock := range mocks {
			if mock.Enabled() {
				select {
				case out <- mock:
				case <-ctx.Done():
					return
				case <-done:
					return
				}
			}
		}
	}()

	return out, nil
}

func (s *InMemMockStore[TMock]) Eligible(ctx context.Context) ([]TMock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	mocks := make([]TMock, 0, len(s.data))
	for _, mock := range s.data {
		if mock.Enabled() {
			mocks = append(mocks, mock)
		}
	}
	return mocks, nil
}

func (s *InMemMockStore[TMock]) Save(_ context.Context, mocks ...TMock) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = append(s.data, mocks...)

	sort.SliceStable(s.data, func(a, b int) bool {
		return s.data[a].GetPriority() < s.data[b].GetPriority()
	})

	return nil
}

func (s *InMemMockStore[TMock]) Get(_ context.Context, id string) (TMock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, datum := range s.data {
		if datum.GetID() == id {
			return datum, nil
		}
	}

	var result TMock
	return result, nil
}

func (s *InMemMockStore[TMock]) GetByIDs(ctx context.Context, ids ...string) ([]TMock, error) {
	mocks := make([]TMock, 0, len(ids))
	for _, id := range ids {
		m, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}

		mocks = append(mocks, m)
	}

	return mocks, nil
}

func (s *InMemMockStore[TMock]) GetAll(_ context.Context) ([]TMock, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]TMock, len(s.data))
	copy(ret, s.data)

	return ret, nil
}

func (s *InMemMockStore[TMock]) Del(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		index := -1
		for i, m := range s.data {
			if m.GetID() == id {
				index = i
				break
			}
		}

		s.data = s.data[:index+copy(s.data[index:], s.data[index+1:])]
	}

	return nil
}

func (s *InMemMockStore[TMock]) DelExt(ctx context.Context) error {
	ids := make([]string, 0, len(s.data))

	for _, m := range s.data {
		if len(m.GetSource()) > 0 {
			ids = append(ids, m.GetID())
		}
	}

	for _, id := range ids {
		if err := s.Del(ctx, id); err != nil {
			return err
		}
	}

	return nil
}

func (s *InMemMockStore[TMock]) DelAll(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data = nil
	s.data = make([]TMock, 0)

	return nil
}
