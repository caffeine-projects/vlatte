package handler

import (
	"net/http"
)

// ListScenarios answers the state of every scenario, in the order of their names: GET /scenarios. A scenario shows
// once a mock started it, or its state was set.
func ListScenarios(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		list, err := ops.ListScenarios()
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, list)
	})
}
