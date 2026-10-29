package handler

import (
	"net/http"
)

// ListKV answers the keys of the key-value store, in order, without their values: GET /kv.
func ListKV(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.ListKV(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, list)
	})
}
