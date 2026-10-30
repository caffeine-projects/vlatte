package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// KVEntry is a key of the key-value store and its value, as text: the answer of [Operations.GetKV], GET /kv/{key}.
type KVEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// GetKV returns the value of key in the key-value store. A key that isn't set is a key_not_found problem. The bytes of
// a key or a value that aren't UTF-8 are replaced, as JSON and YAML can't hold them. It needs a server that keeps a
// key-value store.
func (o *Operations) GetKV(ctx context.Context, key string) (KVEntry, error) {
	if o.kv == nil {
		return KVEntry{}, notSupported(_lackingKV)
	}

	v, err := o.kv.Get(ctx, key)
	switch {
	case errors.Is(err, vlatte.ErrKVKeyNotFound):
		return KVEntry{}, &problemError{
			status: http.StatusNotFound, code: _codeKeyNotFound, detail: fmt.Sprintf("no value is set for the key %q", key),
		}
	case err != nil:
		return KVEntry{}, fmt.Errorf("admin: reading key %q: %w", key, err)
	}

	return kvEntry(key, string(v)), nil
}

// kvEntry is the answer of key and its value.
func kvEntry(key, value string) KVEntry {
	return KVEntry{Key: validUTF8(key), Value: validUTF8(value)}
}
