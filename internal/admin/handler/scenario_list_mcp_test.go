package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestListScenariosTool(t *testing.T) {
	scenarios := matchscenario.New()
	scenarios.SetScenario("checkout", "paid")
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	doc := conformsAs(t, call(t, cs, "list_scenarios", nil), "json", "ScenarioList")
	require.Equal(t, []any{map[string]any{"name": "checkout", "state": "paid"}}, doc["scenarios"])
}
