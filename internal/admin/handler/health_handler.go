package handler

import (
	"net/http"

	"github.com/rs/zerolog"
)

// Health answers that the admin API serves, and what it serves: the kind of the mock server, such as http, and the
// URL it serves its mocks at. GET /health.
func Health(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, http.StatusOK, ops.Health())
	})
}

// Live answers that the admin API serves, as GET /health does: GET /livez. It checks nothing else, so a store that
// is down doesn't get the server restarted. With anonymous, it answers the status alone: anyone may ask it, as the
// probe sends no credentials.
func Live(ops *Operations, anonymous bool) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		respond(w, r, http.StatusOK, probeStatus(ops, anonymous))
	})
}

// Ready answers that the mock store can serve its mocks, as GET /health does, or 503 when it can't: GET /readyz.
// With anonymous, it answers the status alone, and a 503 that doesn't tell why, which it logs.
func Ready(ops *Operations, anonymous bool) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		err := ops.Ready(r.Context())
		if err != nil && anonymous {
			zerolog.Ctx(r.Context()).Error().Err(err).Msg("admin: not ready")
			err = &problemError{
				status: http.StatusServiceUnavailable, code: _codeNotReady, detail: "the mock store isn't ready",
			}
		}

		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, probeStatus(ops, anonymous))
	})
}

// Started answers that the mock server started, as GET /livez does: GET /startupz. The admin API serves only once
// the mock server loaded its mocks, so until then the probe isn't answered.
func Started(ops *Operations, anonymous bool) http.Handler {
	return Live(ops, anonymous)
}

// probeStatus is what a probe answers: the status alone with anonymous, as GET /health does otherwise.
func probeStatus(ops *Operations, anonymous bool) HealthStatus {
	if anonymous {
		return HealthStatus{Status: "ok"}
	}

	return ops.Health()
}
