package vlatte

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
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

// Built-in CSV-based KV implementation

var _ KVReloader = (*csvKV)(nil)

// NewCSVKV returns a [KVReloader] that starts with the keys and values of the CSV file at path, and keeps them in
// memory: [KV.Set] and [KV.Del] change the store, never the file, and [KVReloader.Reload] loads the file again in
// place of every value. The first row of the file is the header key,value, and each other row is a key and its value,
// quoted as RFC 4180 says. Keys and values keep their spaces, an empty value is stored, and a UTF-8 byte order mark
// at the start is dropped. It fails, naming the line, when the header is missing, or when a row doesn't hold two
// fields, has an empty key or repeats a key; the error quotes keys, never values. When the file can't be read, the
// error wraps the one of [os.ReadFile], such as [io/fs.ErrNotExist].
func NewCSVKV(path string) (KVReloader, error) {
	data, err := readCSVKV(path)
	if err != nil {
		return nil, err
	}

	return &csvKV{memKV: &memKV{data: data}, path: path}, nil
}

// MustNewCSVKV is like [NewCSVKV], but panics when the file can't be loaded.
func MustNewCSVKV(path string) KVReloader {
	kv, err := NewCSVKV(path)
	if err != nil {
		panic(err)
	}

	return kv
}

// csvKV is the store of NewCSVKV: a memory store, which Reload fills from its file again.
type csvKV struct {
	*memKV

	path string
}

func (p *csvKV) Reload(context.Context) error {
	// The whole file is read and checked before it replaces the values: a request reads the old values or the new
	// ones, never a mix, and a broken file changes nothing.
	data, err := readCSVKV(p.path)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.data = data
	p.mu.Unlock()

	return nil
}

// readCSVKV reads the keys and values of the CSV file at path, as NewCSVKV describes it.
func readCSVKV(path string) (map[string][]byte, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("kv: reading %s: %w", path, err)
	}

	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(b, []byte("\uFEFF"))))
	r.FieldsPerRecord = -1 // each row is checked below, to say which line to fix

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("kv: %s:1: expected the header key,value, not an empty file", path)
	}

	if err != nil {
		return nil, fmt.Errorf("kv: reading %s: %w", path, err)
	}

	if !slices.Equal(header, []string{"key", "value"}) {
		// The row isn't quoted: in a file without its header, it is a key and its value, which can be a secret.
		line, _ := r.FieldPos(0)
		return nil, fmt.Errorf("kv: %s:%d: expected the header key,value: write it as the file's first row", path, line)
	}

	data := make(map[string][]byte)
	lines := make(map[string]int)

	for {
		row, err := r.Read()
		if errors.Is(err, io.EOF) {
			return data, nil
		}

		if err != nil {
			return nil, fmt.Errorf("kv: reading %s: %w", path, err)
		}

		line, _ := r.FieldPos(0)

		switch key := row[0]; {
		case len(row) != 2:
			return nil, fmt.Errorf("kv: %s:%d: expected 2 fields, a key and its value, not %d", path, line, len(row))
		case key == "":
			return nil, fmt.Errorf("kv: %s:%d: expected a key, not an empty field", path, line)
		case lines[key] > 0:
			return nil, fmt.Errorf("kv: %s:%d: key %q repeats line %d: keep one of them", path, line, key, lines[key])
		default:
			lines[key] = line
			data[key] = []byte(row[1])
		}
	}
}
