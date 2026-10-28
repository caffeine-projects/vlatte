package handler_test

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestScenarios(t *testing.T) {
	// A client reads where each scenario is, with the states its mocks name and the moves they make, moves one to the
	// state its test needs, and starts them over, one or all.
	store := newScenarios(
		[4]string{"pay", "Checkout", "started", "paid"},
		[4]string{"refund", "checkout", "paid", ""},
	)
	base := serve(t, newStore(t), adminapi.Capabilities{Scenarios: store})

	res, body := send(t, request{method: http.MethodPut, url: base + "/scenarios/CHECKOUT", body: `{"state": "paid"}`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	checkout := map[string]any{
		"name":      "Checkout",
		"state":     "paid",
		"instances": []any{},
		"states": []any{
			map[string]any{"name": "started", "reachable": true},
			map[string]any{"name": "paid", "reachable": true},
		},
		"transitions": []any{map[string]any{"from": "started", "to": "paid", "mock_id": "pay"}},
	}
	require.Equal(t, checkout, conforms(t, res, body, "Scenario"), "any case moves it, under the name of its mocks")

	res, body = send(t, request{method: http.MethodPut, url: base + "/scenarios/" + url.PathEscape("Check out"), contentType: "application/yaml", body: "state: on\n"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	res, body = send(t, request{method: http.MethodGet, url: base + "/scenarios"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []any{
		map[string]any{"name": "Check out", "state": "on", "instances": []any{}, "states": []any{}, "transitions": []any{}},
		checkout,
	}, conforms(t, res, body, "ScenarioList")["scenarios"])

	res, body = send(t, request{method: http.MethodDelete, url: base + "/scenarios/checkout"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, map[string]string{"Checkout": "started", "Check out": "on"}, store.store.Scenarios())

	res, body = send(t, request{method: http.MethodDelete, url: base + "/scenarios"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, map[string]string{"Checkout": "started", "Check out": "started"}, store.store.Scenarios())
}

func TestScenarios_AStateNoMockNamesIsRefused(t *testing.T) {
	// A state no mock of the scenario requires or moves to leaves every mock of it unmatched: the client learns at
	// once, and which states there are.
	store := newScenarios([4]string{"pay", "checkout", "started", "paid"})
	base := serve(t, newStore(t), adminapi.Capabilities{Scenarios: store})

	res, body := send(t, request{method: http.MethodPut, url: base + "/scenarios/checkout", body: `{"state": "payed"}`})

	doc := problem(t, res, body, http.StatusBadRequest, "unknown_state")
	require.Equal(t, `scenario: unknown state: checkout has no state "payed"; its states are paid, started`, doc["detail"])
	require.Empty(t, store.store.Scenarios(), "nothing moved")
}

func TestScenarios_ANewStateIsRequired(t *testing.T) {
	store := newScenarios()
	base := serve(t, newStore(t), adminapi.Capabilities{Scenarios: store})

	for _, body := range []string{``, `{}`, `{"state": ""}`, `{"state": "x", "name": "y"}`} {
		res, answer := send(t, request{method: http.MethodPut, url: base + "/scenarios/s", body: body})

		problem(t, res, answer, http.StatusBadRequest, "invalid_body")
	}

	require.Empty(t, store.store.Scenarios(), "nothing moved")
}

func TestScenarios_AServerThatFailsAnswersAnError(t *testing.T) {
	// A server whose mocks can't be listed fails the request, rather than answer no scenario, or a move it can't show.
	down := errors.New("store down")

	for _, tt := range []struct {
		name  string
		store *fakeScenarios
		req   request
		want  string
	}{
		{
			"listing", &fakeScenarios{store: matchscenario.New(), listErr: down},
			request{method: http.MethodGet},
			"admin: listing scenarios: store down",
		},
		{
			"moving", &fakeScenarios{store: matchscenario.New(), setErr: down},
			request{method: http.MethodPut, body: `{"state": "paid"}`},
			"admin: setting scenario checkout: store down",
		},
		{
			"showing the move", &fakeScenarios{store: matchscenario.New(), listErr: down},
			request{method: http.MethodPut, body: `{"state": "paid"}`},
			"admin: listing scenarios: store down",
		},
		{
			"a move not listed", &fakeScenarios{store: matchscenario.New(), unlisted: true},
			request{method: http.MethodPut, body: `{"state": "paid"}`},
			"admin: scenario checkout is not listed after it moved",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := serve(t, newStore(t), adminapi.Capabilities{Scenarios: tt.store})
			tt.req.url = base + "/scenarios"
			if tt.req.method == http.MethodPut {
				tt.req.url += "/checkout"
			}

			res, body := send(t, tt.req)

			doc := problem(t, res, body, http.StatusInternalServerError, "internal")
			require.Equal(t, tt.want, doc["detail"])
		})
	}
}

func TestSetScenario_AKeyMovesTheInstanceOfThatKey(t *testing.T) {
	// A test that sends a key of its own moves its instance only: the shared one, and other tests', stay.
	store := newScenarios([4]string{"pay", "checkout", "started", "paid"})
	base := serve(t, newStore(t), adminapi.Capabilities{Scenarios: store})

	res, body := send(t, request{method: http.MethodPut, url: base + "/scenarios/checkout?key=t1", body: `{"state": "paid"}`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	doc := conforms(t, res, body, "Scenario")
	require.Equal(t, "started", doc["state"])
	require.Equal(t, []any{map[string]any{"key": "t1", "state": "paid"}}, doc["instances"])
	require.Equal(t, "paid", store.store.State("checkout", matchscenario.Key("t1")))
}
