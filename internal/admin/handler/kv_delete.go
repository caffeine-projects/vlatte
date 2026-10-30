package handler

import (
	"context"
	"fmt"
)

// DeleteKV removes key from the key-value store: a template that reads it next fails, as for a key never set. A key
// that isn't set is no problem, so a test's cleanup can remove a key twice. It needs a server that keeps a key-value
// store.
func (o *Operations) DeleteKV(ctx context.Context, key string) error {
	if o.kv == nil {
		return notSupported(_lackingKV)
	}

	if err := o.kv.Del(ctx, key); err != nil {
		return fmt.Errorf("admin: removing key %q: %w", key, err)
	}

	return nil
}
