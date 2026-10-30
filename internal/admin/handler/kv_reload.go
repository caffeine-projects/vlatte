package handler

import (
	"context"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// ReloadKV loads the key-value store again from its source, such as the file of [vlatte.NewCSVKV], in place of every
// value: values set since the last load are gone, and keys removed since come back. When the source can't be loaded,
// a load_failed problem says why, such as the line of the file to fix, and the store keeps the values it had. It
// needs a server whose store is a [vlatte.KVReloader].
func (o *Operations) ReloadKV(ctx context.Context) error {
	kv, ok := o.kv.(vlatte.KVReloader)
	if !ok {
		return notSupported(_lackingKVReload)
	}

	if err := kv.Reload(ctx); err != nil {
		return &problemError{status: http.StatusInternalServerError, code: _codeLoadFailed, detail: err.Error()}
	}

	return nil
}
