package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte/match/matchscenario"
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

// ResetScenario moves the scenario of the path's name back to where it started: DELETE /scenarios/{name}. The query
// parameter key, even empty, resets the instance of that key only.
func ResetScenario(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := ops.ResetScenario(r.PathValue("name"), scenarioInstance(r)...); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// scenarioInstance returns the instance of a scenario the query parameter key of r picks, or none without one.
func scenarioInstance(r *http.Request) []matchscenario.Instance {
	query := r.URL.Query()
	if !query.Has("key") {
		return nil
	}

	return []matchscenario.Instance{matchscenario.Key(query.Get("key"))}
}
