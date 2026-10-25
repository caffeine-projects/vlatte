package handler

import (
	"net/http"
)

// UpdateMock changes the state of the mock of the path's id, and answers it: PATCH /mocks/{id}. A disabled mock
// matches no request, until enabled again. The mock is stored again with its new state, which a persistent store
// keeps: when the store refuses it, the mock keeps the state it had.
func UpdateMock(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		summary, err := ops.UpdateMock(r.Context(), r.PathValue("id"), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, summary)
	})
}
