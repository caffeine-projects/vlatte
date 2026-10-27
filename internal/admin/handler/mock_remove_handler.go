package handler

import (
	"net/http"
)

// RemoveMock removes the mock of the path's id, and closes it: DELETE /mocks/{id}.
func RemoveMock(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.RemoveMock(r.Context(), r.PathValue("id")); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
