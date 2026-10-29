// Package kvview gives templates the key-value store of a mock server, as text.
package kvview

import (
	"context"
	"errors"
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// View gives templates the values of a key-value store as text: {{ .KV.Get "key" }}. A key that may be missing reads
// with Has first: {{ if .KV.Has "key" }}.
type View struct {
	ctx context.Context
	kv  vlatte.KV
}

// New returns the View of kv, read with ctx.
func New(ctx context.Context, kv vlatte.KV) *View {
	return &View{ctx: ctx, kv: kv}
}

// Get returns the value stored under key as text. A key that isn't stored is an error, as a missing map key is, so a
// typo doesn't render as an empty value.
func (v *View) Get(key string) (string, error) {
	b, err := v.kv.Get(v.ctx, key)
	if errors.Is(err, vlatte.ErrKVKeyNotFound) {
		return "", fmt.Errorf("key %q is not set: set it, or check for it with .KV.Has", key)
	}

	if err != nil {
		return "", fmt.Errorf("reading key %q: %w", key, err)
	}

	return string(b), nil
}

// Has reports whether a value is stored under key.
func (v *View) Has(key string) (bool, error) {
	ok, err := v.kv.Has(v.ctx, key)
	if err != nil {
		return false, fmt.Errorf("reading key %q: %w", key, err)
	}

	return ok, nil
}
