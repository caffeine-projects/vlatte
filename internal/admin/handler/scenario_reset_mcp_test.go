package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestResetScenariosTool(t *testing.T) {
	scenarios := matchscenario.New()
	scenarios.SetScenario("checkout", "paid")
	cs := connect(t, newStore(t), adminapi.Capabilities{Scenarios: scenarios})

	require.Equal(t, "done", call(t, cs, "reset_scenarios", nil))
	require.Equal(t, map[string]string{"checkout": "started"}, scenarios.Scenarios())
}
