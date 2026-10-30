package handler

import (
	"context"
	"fmt"
	"slices"
)

// KVKeys is the answer of [Operations.ListKV]: GET /kv.
type KVKeys struct {
	Keys []string `json:"keys"`
}

// ListKV returns the keys of the key-value store, in order, without their values. A key's bytes that aren't UTF-8 are
// replaced, as JSON and YAML can't hold them. It needs a server that keeps a key-value store.
func (o *Operations) ListKV(ctx context.Context) (KVKeys, error) {
	if o.kv == nil {
		return KVKeys{}, notSupported(_lackingKV)
	}

	keys, err := o.kv.Keys(ctx)
	if err != nil {
		return KVKeys{}, fmt.Errorf("admin: listing keys: %w", err)
	}

	list := KVKeys{Keys: make([]string, 0, len(keys))}
	for _, k := range keys {
		list.Keys = append(list.Keys, validUTF8(k))
	}

	slices.Sort(list.Keys)

	return list, nil
}
