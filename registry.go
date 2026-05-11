package vlatte

import (
	"context"
	"fmt"
	"sync"
)

type Registry[B Mock, A any] struct {
	mu     sync.RWMutex
	flavor A
	store  Store[B]
	scopes []*Scope[B]
}

func (a *Registry[B, A]) Add(builders ...Builder[B, A]) (*Scope[B], error) {
	ground := make([]B, len(builders))

	for i, b := range builders {
		built, err := b.Build(a.flavor)
		if err != nil {
			return nil, fmt.Errorf("server: error adding mock at index %d.\n%w", i, err)
		}

		if err := a.store.Store(context.Background(), built); err != nil {
			return nil, err
		}

		ground[i] = built
	}

	scope := NewScope(ground, a.store)

	a.mu.Lock()
	a.scopes = append(a.scopes, scope)
	a.mu.Unlock()

	return scope, nil
}

func (a *Registry[B, A]) Scopes() []*Scope[B] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ret := make([]*Scope[B], len(a.scopes))
	copy(ret, a.scopes)

	return ret
}
