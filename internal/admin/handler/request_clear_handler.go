package handler

import (
	"net/http"
)

// ClearRequests removes every request of the journal: DELETE /requests.
func ClearRequests(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ClearRequests(); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
