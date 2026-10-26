package handler

import (
	"net/http"
)

// ReplaceMock swaps the mock of the path's id for the mock of the request body, in one step, so a request finds one
// or the other, and answers its state: PUT /mocks/{id}. The body's id must be the path's. The replaced mock is closed.
func ReplaceMock(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		summary, err := ops.ReplaceMock(r.Context(), r.PathValue("id"), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, summary)
	})
}
