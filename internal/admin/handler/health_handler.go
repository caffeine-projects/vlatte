package handler

import (
	"net/http"
)

// Health answers that the admin API serves, and what it serves: the kind of the mock server, such as http, and the
// URL it serves its mocks at. GET /health.
func Health(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, http.StatusOK, ops.Health())
	})
}
