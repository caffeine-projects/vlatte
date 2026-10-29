package handler

import (
	"net/http"
)

// DeleteKV removes the path's key from the key-value store: DELETE /kv/{key}. A key that isn't set answers 204 too. A
// key with a slash comes escaped, as /kv/a%2Fb.
func DeleteKV(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.DeleteKV(r.Context(), r.PathValue("key")); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
