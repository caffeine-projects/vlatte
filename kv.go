package vlatte

import (
	"context"
	"sync"
)

// KV defines a contract for a generic parameters repository.
type KV interface {
	// Get returns the parameter by its key.
	Get(ctx context.Context, k string) ([]byte, error)

	// Has checks if a parameter with the given key exists.
	Has(ctx context.Context, k string) (bool, error)

	// Set sets a parameter.
	Set(ctx context.Context, k string, v []byte) error

	// Del removes a parameter by its key.
	Del(ctx context.Context, k string) error
}

var _ KV = (*memKV)(nil)

type memKV struct {
	data map[string][]byte
	mu   sync.RWMutex
}

// NewMemoryKV returns a KV that keeps the parameters in memory.
func NewMemoryKV() KV {
	return &memKV{data: make(map[string][]byte)}
}

func (p *memKV) Get(_ context.Context, k string) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.data[k], nil
}

func (p *memKV) Set(_ context.Context, k string, v []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.data[k] = v

	return nil
}

func (p *memKV) Del(_ context.Context, k string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.data, k)

	return nil
}

func (p *memKV) Has(_ context.Context, k string) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	_, ok := p.data[k]

	return ok, nil
}
