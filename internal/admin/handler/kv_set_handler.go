package handler

import (
	"net/http"
)

// SetKV sets the path's key, in the key-value store, to the value of the request body, and answers it: PUT
// /kv/{key}. Templates read it from the next request on. A key with a slash comes escaped, as /kv/a%2Fb.
func SetKV(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		entry, err := ops.SetKV(r.Context(), r.PathValue("key"), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, entry)
	})
}
