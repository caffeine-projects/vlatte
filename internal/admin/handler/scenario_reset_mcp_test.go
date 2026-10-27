package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestResetScenariosTool(t *testing.T) {
	scenarios := newScenarios()
	scenarios.store.SetScenario("checkout", "paid")
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	require.Equal(t, "done", call(t, cs, "reset_scenarios", nil))
	require.Equal(t, map[string]string{"checkout": "started"}, scenarios.store.Scenarios())
}

func TestResetScenarioTool(t *testing.T) {
	scenarios := newScenarios()
	scenarios.store.SetScenario("checkout", "paid")
	scenarios.store.SetScenario("refund", "asked")
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	require.Equal(t, "done", call(t, cs, "reset_scenario", map[string]any{"name": "Checkout"}))
	require.Equal(t, map[string]string{"checkout": "started", "refund": "asked"}, scenarios.store.Scenarios())
}
