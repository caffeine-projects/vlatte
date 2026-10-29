package handler

import (
	"context"
	"fmt"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// kvChange is the body of PUT /kv/{key}.
type kvChange struct {
	value *string
}

var _kvChangeSchema = config.NewSchema(
	config.Field[kvChange]{
		Key: "value", Kind: config.String, Usage: "The value of the key, as text. An empty one is a value.",
		Set: func(c *kvChange, v config.Value) error { c.value = new(v.Str); return nil },
	},
)

// SetKV sets key, in the key-value store, to the value of doc, a document in format, such as {"value": "abc123"},
// and returns it: templates read it from the next request on. An empty value is a value, which .KV.Has reports. A
// document without a value is an invalid_body problem. It needs a server that keeps a key-value store.
func (o *Operations) SetKV(ctx context.Context, key string, doc []byte, format string) (KVEntry, error) {
	if o.kv == nil {
		return KVEntry{}, notSupported(_lackingKV)
	}

	var change kvChange
	if err := decodeBody(_kvChangeSchema, &change, doc, format); err != nil {
		return KVEntry{}, invalid("the body", err)
	}

	if change.value == nil {
		return KVEntry{}, invalid("the body", &config.Error{Problems: []config.Problem{{
			Code: config.CodeMissingValue, Origin: "admin request", Key: "value", Message: "value is required",
		}}})
	}

	return o.setKV(ctx, key, *change.value)
}

// setKV sets key, in the key-value store, to value, and returns it.
func (o *Operations) setKV(ctx context.Context, key, value string) (KVEntry, error) {
	if err := o.kv.Set(ctx, key, []byte(value)); err != nil {
		return KVEntry{}, fmt.Errorf("admin: setting key %q: %w", key, err)
	}

	return kvEntry(key, value), nil
}
