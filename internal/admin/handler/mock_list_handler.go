package handler

import (
	"net/http"
)

// ListMocks answers the mocks of the server, enabled or not, in the order they match: GET /mocks.
func ListMocks(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.ListMocks(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, list)
	})
}
