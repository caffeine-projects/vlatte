package handler

import (
	"net/http"
)

// ReloadKV loads the key-value store again from its file, in place of every value: POST /kv/reload. When the file has
// a problem, the store keeps the values it had.
func ReloadKV(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ReloadKV(r.Context()); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
