package handler

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// scenario is the state of a scenario.
type scenario struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// scenarioList is the answer of GET /scenarios.
type scenarioList struct {
	Scenarios []scenario `json:"scenarios"`
}

// scenarioChange is the body of PUT /scenarios/{name}.
type scenarioChange struct {
	state string
}

var _scenarioChangeSchema = config.NewSchema(
	config.Field[scenarioChange]{
		Key: "state", Kind: config.String, Usage: "The state the scenario moves to.",
		Set: func(c *scenarioChange, v config.Value) error { c.state = v.Str; return nil },
	},
)

// ListScenarios answers the state of every scenario, in the order of their names: GET /scenarios. A scenario shows
// once a mock started it, or its state was set.
func ListScenarios(scenarios Scenarios) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		states := scenarios.Scenarios()

		list := scenarioList{Scenarios: make([]scenario, 0, len(states))}
		for name, state := range states {
			list.Scenarios = append(list.Scenarios, scenario{Name: name, State: state})
		}

		slices.SortFunc(list.Scenarios, func(a, b scenario) int { return cmp.Compare(a.Name, b.Name) })

		respond(w, r, http.StatusOK, list)
	})
}

// SetScenario moves the scenario of the path's name to the state of the request body, and answers it: PUT
// /scenarios/{name}. The mocks that require that state match next. Names are exact: Checkout isn't checkout.
func SetScenario(scenarios Scenarios) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		var change scenarioChange
		if err := decodeBody(_scenarioChangeSchema, &change, data, format); err != nil {
			fail(w, r, invalid("the body", err))
			return
		}

		if change.state == "" {
			fail(w, r, invalid("the body", &config.Error{Problems: []config.Problem{{
				Code: config.CodeMissingValue, Origin: "admin request", Key: "state", Message: "state is required",
			}}}))

			return
		}

		name := r.PathValue("name")
		scenarios.SetScenario(name, change.state)

		respond(w, r, http.StatusOK, scenario{Name: name, State: change.state})
	})
}

// ResetScenarios moves every scenario back to where it started: DELETE /scenarios.
func ResetScenarios(scenarios Scenarios) http.Handler {
	return negotiated(func(w http.ResponseWriter, _ *http.Request) {
		scenarios.ResetScenarios()
		w.WriteHeader(http.StatusNoContent)
	})
}
