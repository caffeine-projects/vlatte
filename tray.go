package vlatte

import (
	"context"
	"fmt"
	"sync"
)

type Tray[B Bean, A MockApp[B]] struct {
	mu       sync.RWMutex
	drink    A
	canister Canister[B]
	mugs     []*Cup[B]
}

func (a *Tray[B, A]) Add(grinder ...Grinder[B, A]) (*Cup[B], error) {
	added := make([]B, len(grinder))

	for i, b := range grinder {
		mock, err := b.Grind(a.drink)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		mock.Prepare()

		if err := a.canister.Store(context.Background(), mock); err != nil {
			return nil, err
		}

		added[i] = mock
	}

	mug := Pour(a.canister, added)

	a.mu.Lock()
	a.mugs = append(a.mugs, mug)
	a.mu.Unlock()

	return mug, nil
}

func (a *Tray[B, A]) Mugs() []*Cup[B] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ret := make([]*Cup[B], len(a.mugs))
	copy(ret, a.mugs)

	return ret
}
