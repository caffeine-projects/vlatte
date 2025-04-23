package vlatte

import (
	"context"
	"sort"
	"sync"
)

var _ Canister[*GreenBean] = (*Jar[*GreenBean])(nil)

type Jar[B Bean] struct {
	beans []B
	mu    sync.RWMutex
}

func GrabCanister[B Bean]() Canister[B] {
	return &Jar[B]{beans: make([]B, 0)}
}

func (s *Jar[B]) ScoopCuratedStream(ctx context.Context, done <-chan struct{}) (<-chan B, error) {
	s.mu.RLock()
	beans := make([]B, len(s.beans))
	copy(beans, s.beans)
	s.mu.RUnlock()

	out := make(chan B, len(beans))
	go func() {
		defer close(out)
		for _, bean := range beans {
			if bean.Roasted() {
				select {
				case out <- bean:
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

func (s *Jar[B]) ScoopCurated(_ context.Context) ([]B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	beans := make([]B, 0, len(s.beans))
	for _, bean := range s.beans {
		if bean.Roasted() {
			beans = append(beans, bean)
		}
	}

	return beans, nil
}

func (s *Jar[B]) Store(_ context.Context, beans ...B) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.beans = append(s.beans, beans...)

	sort.SliceStable(s.beans, func(a, b int) bool {
		return s.beans[a].GetPriority() < s.beans[b].GetPriority()
	})

	return nil
}

func (s *Jar[B]) Pick(_ context.Context, id string) (B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, datum := range s.beans {
		if datum.GetID() == id {
			return datum, nil
		}
	}

	var result B
	return result, nil
}

func (s *Jar[B]) PickAll(_ context.Context) ([]B, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ret := make([]B, len(s.beans))
	copy(ret, s.beans)

	return ret, nil
}

func (s *Jar[B]) Toss(_ context.Context, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		index := -1
		for i, m := range s.beans {
			if m.GetID() == id {
				index = i
				break
			}
		}

		s.beans = s.beans[:index+copy(s.beans[index:], s.beans[index+1:])]
	}

	return nil
}

func (s *Jar[B]) Empty(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.beans = nil
	s.beans = make([]B, 0)

	return nil
}
