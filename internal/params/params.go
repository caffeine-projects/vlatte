// Package params gives templates the parameters of a mock server, as text.
package params

import (
	"context"
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Parameters gives templates the app parameters as text: {{ .Parameters.Get "key" }}. A parameter that may be missing
// reads with Has first: {{ if .Parameters.Has "key" }}.
type Parameters struct {
	ctx context.Context
	kv  vlatte.KV
}

// New returns the Parameters of kv, read with ctx.
func New(ctx context.Context, kv vlatte.KV) *Parameters {
	return &Parameters{ctx: ctx, kv: kv}
}

// Get returns the value stored under key as text. A key that isn't stored is an error, as a missing map key is, so a
// typo doesn't render as an empty value.
func (p *Parameters) Get(key string) (string, error) {
	ok, err := p.Has(key)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf("parameters: no parameter %q", key)
	}

	b, err := p.kv.Get(p.ctx, key)

	return string(b), err
}

// Has reports whether a value is stored under key.
func (p *Parameters) Has(key string) (bool, error) {
	return p.kv.Has(p.ctx, key)
}
