package handler

import (
	"net/http"
)

// GetRequest answers the request of the path's id: GET /requests/{id}.
func GetRequest(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		entry, err := ops.GetRequest(r.PathValue("id"))
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, entry)
	})
}
