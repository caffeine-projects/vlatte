package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestSetScenarioTool(t *testing.T) {
	// An agent moves a scenario to the state its test needs.
	scenarios := newScenarios()
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	doc := conformsAs(t, call(t, cs, "set_scenario", map[string]any{"name": "Check out", "state": "paid"}), "json", "Scenario")
	require.Equal(t, map[string]any{"name": "Check out", "state": "paid", "instances": []any{}, "states": []any{}, "transitions": []any{}}, doc)
	require.Equal(t, map[string]string{"Check out": "paid"}, scenarios.store.Scenarios())

	callFails(t, cs, "set_scenario", map[string]any{"name": "Check out", "state": ""}, http.StatusBadRequest, "invalid_body")
	require.Equal(t, map[string]string{"Check out": "paid"}, scenarios.store.Scenarios(), "nothing moved")
}

func TestSetScenarioTool_AKeyMovesTheInstanceOfThatKey(t *testing.T) {
	scenarios := newScenarios()
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	doc := conformsAs(t, call(t, cs, "set_scenario", map[string]any{"name": "checkout", "state": "paid", "key": "t1"}), "json", "Scenario")
	require.Equal(t, []any{map[string]any{"key": "t1", "state": "paid"}}, doc["instances"])
	require.Equal(t, "paid", scenarios.store.State("checkout", matchscenario.Key("t1")))
	require.Empty(t, scenarios.store.Scenarios(), "the shared instance didn't move")
}
