package kv

import "context"

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
