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
