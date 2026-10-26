package handler

import (
	"net/http"
)

// ReloadMocks loads the mock files of the server again: POST /mocks/reload. When a file has problems, the server
// serves what it served before.
func ReloadMocks(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ReloadMocks(); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
