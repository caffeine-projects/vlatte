package vlatte

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

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
