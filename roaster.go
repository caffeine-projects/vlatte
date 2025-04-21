package vlatte

import (
	"context"
	"fmt"
	"sync"
)

type Roaster[TM Mock, A MockApp[TM]] struct {
	mu     sync.RWMutex
	app    A
	store  Store[TM]
	scopes []*Scope[TM]
}

func (a *Roaster[TM, A]) Add(builders ...Builder[TM, A]) (*Scope[TM], error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	added := make([]TM, len(builders))

	for i, b := range builders {
		mock, err := b.Build(a.app)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		mock.Prepare()

		if err := a.store.Save(context.Background(), mock); err != nil {
			return nil, err
		}

		added[i] = mock
	}

	scope := Scoped(a.store, added)
	a.scopes = append(a.scopes, scope)

	return scope, nil
}

func (a *Roaster[TM, A]) List() []*Scope[TM] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.scopes
}
