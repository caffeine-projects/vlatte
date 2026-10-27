package handler

import (
	"net/http"
)

// ValidateMock builds the mock of the request body, as POST /mocks would, and answers its state without adding it:
// POST /mocks/validate. A mock with an id in use is a conflict, as it would be for POST /mocks.
func ValidateMock(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		summary, err := ops.ValidateMock(r.Context(), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, summary)
	})
}
