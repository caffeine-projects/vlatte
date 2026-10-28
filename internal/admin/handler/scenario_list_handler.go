package handler

import (
	"net/http"
)

// ListScenarios answers every scenario, in the order of their names, with its state, the states its mocks name and
// the moves they make: GET /scenarios.
func ListScenarios(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.ListScenarios(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, list)
	})
}
