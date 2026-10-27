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
	scenarios := matchscenario.New()
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	doc := conformsAs(t, call(t, cs, "set_scenario", map[string]any{"name": "Check out", "state": "paid"}), "json", "Scenario")
	require.Equal(t, map[string]any{"name": "Check out", "state": "paid"}, doc)
	require.Equal(t, map[string]string{"Check out": "paid"}, scenarios.Scenarios())

	callFails(t, cs, "set_scenario", map[string]any{"name": "Check out", "state": ""}, http.StatusBadRequest, "invalid_body")
	require.Equal(t, map[string]string{"Check out": "paid"}, scenarios.Scenarios(), "nothing moved")
}
