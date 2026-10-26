package handler_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
)

func TestScenarios(t *testing.T) {
	// A client reads where each scenario is, moves one to the state its test needs, and starts them all over.
	store := mfeat.NewScenarioStore()
	base := serve(t, newStore(t), admin.Scenarios(store))

	res, body := send(t, request{method: http.MethodPut, url: base + "/scenarios/" + url.PathEscape("Check out"), body: `{"state": "paid"}`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"name": "Check out", "state": "paid"}, conforms(t, res, body, "Scenario"))

	res, body = send(t, request{method: http.MethodPut, url: base + "/scenarios/a", contentType: "application/yaml", body: "state: started\n"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	res, body = send(t, request{method: http.MethodGet, url: base + "/scenarios"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []any{
		map[string]any{"name": "Check out", "state": "paid"},
		map[string]any{"name": "a", "state": "started"},
	}, conforms(t, res, body, "ScenarioList")["scenarios"])

	res, body = send(t, request{method: http.MethodDelete, url: base + "/scenarios"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, map[string]string{"Check out": "started", "a": "started"}, store.Scenarios())
}

func TestScenarios_ANewStateIsRequired(t *testing.T) {
	store := mfeat.NewScenarioStore()
	base := serve(t, newStore(t), admin.Scenarios(store))

	for _, body := range []string{``, `{}`, `{"state": ""}`, `{"state": "x", "name": "y"}`} {
		res, answer := send(t, request{method: http.MethodPut, url: base + "/scenarios/s", body: body})

		problem(t, res, answer, http.StatusBadRequest, "invalid_body")
	}

	require.Empty(t, store.Scenarios(), "nothing moved")
}
