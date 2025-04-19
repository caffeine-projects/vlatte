package kvmem

import (
	"context"
	"sync"

	"github.com/caffeine-projects/vlatte/kcore/kv"
)

var _ kv.KV = (*memKV)(nil)

type memKV struct {
	data map[string][]byte
	mu   sync.RWMutex
}

func New() kv.KV {
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
