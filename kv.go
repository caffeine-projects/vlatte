package vlatte

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// KV stores the values templates read as .KV, by key. A server reads it for every request it serves, so it must be
// safe for concurrent use. ctx is the request's, or an event's own, which outlives a client that leaves.
type KV interface {
	// Get returns a copy of the value stored under k, or an error that wraps [ErrKVKeyNotFound] when there is none.
	Get(ctx context.Context, k string) ([]byte, error)

	// Has reports whether a value is stored under k. An empty value is stored.
	Has(ctx context.Context, k string) (bool, error)

	// Set stores a copy of v under k, replacing the value there.
	Set(ctx context.Context, k string, v []byte) error

	// Del removes the value under k. Removing a key that isn't stored is not an error.
	Del(ctx context.Context, k string) error

	// Keys returns every key stored, in no set order.
	Keys(ctx context.Context) ([]string, error)
}

// KVReloader is a [KV] that loads its values again from where they came from, such as the file of [NewCSVKV]. The
// admin API's POST /kv/reload reloads one.
type KVReloader interface {
	KV

	// Reload replaces every value with those of the store's source: values set since the last load are gone, and
	// keys removed since come back. When the source can't be loaded, it returns the error and keeps the values it had.
	Reload(ctx context.Context) error
}

// ErrKVKeyNotFound is the error of [KV.Get] for a key that isn't stored.
var ErrKVKeyNotFound = errors.New("kv: key not found")

var _ KV = (*memKV)(nil)

type memKV struct {
	data map[string][]byte
	mu   sync.RWMutex
}

// NewMemoryKV returns a KV that keeps its values in memory.
func NewMemoryKV() KV {
	return &memKV{data: make(map[string][]byte)}
}

func (p *memKV) Get(_ context.Context, k string) ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	v, ok := p.data[k]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrKVKeyNotFound, k)
	}

	return bytes.Clone(v), nil
}

func (p *memKV) Set(_ context.Context, k string, v []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.data[k] = bytes.Clone(v)

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

func (p *memKV) Keys(_ context.Context) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return slices.Collect(maps.Keys(p.data)), nil
}
