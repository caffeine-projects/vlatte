package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestListScenariosTool(t *testing.T) {
	scenarios := newScenarios()
	scenarios.store.SetScenario("checkout", "paid")
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	doc := conformsAs(t, call(t, cs, "list_scenarios", nil), "json", "ScenarioList")
	require.Equal(t, []any{
		map[string]any{"name": "checkout", "state": "paid", "instances": []any{}, "states": []any{}, "transitions": []any{}},
	}, doc["scenarios"])
}
