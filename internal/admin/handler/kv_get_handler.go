package handler

import (
	"net/http"
)

// GetKV answers the value of the path's key in the key-value store: GET /kv/{key}. A key with a slash comes
// escaped, as /kv/a%2Fb.
func GetKV(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		entry, err := ops.GetKV(r.Context(), r.PathValue("key"))
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, entry)
	})
}
