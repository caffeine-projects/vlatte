package vlatte

import (
	"context"
	"fmt"
	"sync"
)

type Tray[TM Bean, A MockApp[TM]] struct {
	mu    sync.RWMutex
	drink A
	jar   Jar[TM]
	mugs  []*Mug[TM]
}

func (a *Tray[TM, A]) Add(builders ...Roaster[TM, A]) (*Mug[TM], error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	added := make([]TM, len(builders))

	for i, b := range builders {
		mock, err := b.Roast(a.drink)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		mock.Prepare()

		if err := a.jar.Save(context.Background(), mock); err != nil {
			return nil, err
		}

		added[i] = mock
	}

	mug := Pour(a.jar, added)
	a.mugs = append(a.mugs, mug)

	return mug, nil
}

func (a *Tray[TM, A]) Mugs() []*Mug[TM] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ret := make([]*Mug[TM], len(a.mugs))
	copy(ret, a.mugs)

	return ret
}
