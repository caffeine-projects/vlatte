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

// Live answers that the admin API serves, as GET /health does: GET /livez. It checks nothing else, so a store that
// is down doesn't get the server restarted.
func Live(ops *Operations) http.Handler {
	return Health(ops)
}

// Ready answers that the mock store can serve its mocks, as GET /health does, or 503 when it can't: GET /readyz.
func Ready(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.Ready(r.Context()); err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, ops.Health())
	})
}

// Started answers that the mock server started, as GET /health does: GET /startupz. The admin API serves only once
// the mock server loaded its mocks, so until then the probe isn't answered.
func Started(ops *Operations) http.Handler {
	return Health(ops)
}
