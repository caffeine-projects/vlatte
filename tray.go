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
	cups     []*Cup[B]
}

func (a *Tray[B, A]) Add(grinder ...Grinder[B, A]) (*Cup[B], error) {
	ground := make([]B, len(grinder))

	for i, b := range grinder {
		b, err := b.Grind(a.drink)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		b.Prepare()

		if err := a.canister.Store(context.Background(), b); err != nil {
			return nil, err
		}

		ground[i] = b
	}

	cup := Pour(Coffee(ground), a.canister)

	a.mu.Lock()
	a.cups = append(a.cups, cup)
	a.mu.Unlock()

	return cup, nil
}

func (a *Tray[B, A]) Mugs() []*Cup[B] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ret := make([]*Cup[B], len(a.cups))
	copy(ret, a.cups)

	return ret
}
