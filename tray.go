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
	mugs     []*Mug[B]
}

func (a *Tray[B, A]) Add(builders ...Roaster[B, A]) (*Mug[B], error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	added := make([]B, len(builders))

	for i, b := range builders {
		mock, err := b.Roast(a.drink)
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
	a.mugs = append(a.mugs, mug)

	return mug, nil
}

func (a *Tray[B, A]) Mugs() []*Mug[B] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ret := make([]*Mug[B], len(a.mugs))
	copy(ret, a.mugs)

	return ret
}
