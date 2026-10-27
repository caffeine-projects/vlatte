package handler

import (
	"net/http"
)

// ResetScenarios moves every scenario back to where it started: DELETE /scenarios.
func ResetScenarios(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ResetScenarios(); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// ResetScenario moves the scenario of the path's name back to where it started: DELETE /scenarios/{name}.
func ResetScenario(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ResetScenario(r.PathValue("name")); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
