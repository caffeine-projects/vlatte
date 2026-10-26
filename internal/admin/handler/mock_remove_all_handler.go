package handler

import (
	"net/http"
)

// RemoveAllMocks removes every mock, of files and code too, and closes them: DELETE /mocks.
func RemoveAllMocks(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.RemoveAllMocks(r.Context()); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
