package vlatte

import (
	"context"
	"sync"
)

type Sleeve[B Bean] struct {
	mu       sync.RWMutex
	cup      *Cup[B]
	canister Canister[B]
	del      map[string]struct{}
}

// Toss removes a by ID, as long as it is scoped by this instance.
func (s *Sleeve[B]) Toss(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.cup.Bean(id)
	if !ok {
		return false
	}

	delete(s.beans, id)
	s.del[id] = struct{}{}

	return true
}

// Empty all scoped Mock instances.
func (s *Sleeve[B]) Empty(ctx context.Context) error {
	for id := range s.cup.Beans() {
		s.Toss(id)
	}

	return s.Sync(ctx)
}

func (s *Sleeve[B]) Sync(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	beans := s.cup.Beans()
	bc := len(beans)
	mocks := make([]B, 0, bc)
	for _, v := range beans {
		mocks = append(mocks, v)
	}

	if err := s.canister.Store(ctx, mocks...); err != nil {
		return err
	}

	ids := make([]string, 0, bc)
	for k := range s.del {
		ids = append(ids, k)
	}

	for _, id := range ids {
		err := s.canister.Toss(ctx, id)
		if err != nil {
			return err
		}

		delete(s.del, id)
	}

	return nil
}
