package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestResetScenarios_OnOperationsThatKeepNoScenarios(t *testing.T) {
	// The handler, mounted apart from the admin API's routes on a server without scenarios, answers what its route
	// would: a 501 problem, never a 204 that tells a test its scenarios started over.
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil)})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/scenarios", nil)
	rec := httptest.NewRecorder()

	handler.ResetScenarios(ops).ServeHTTP(rec, req)

	res := rec.Result()
	res.Request = req
	doc := problem(t, res, rec.Body.String(), http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}

func TestResetScenario_OnOperationsThatKeepNoScenarios(t *testing.T) {
	// As for every scenario: a 501 problem, never a 204 that tells a test its scenario started over.
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil)})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/scenarios/checkout", nil)
	rec := httptest.NewRecorder()

	handler.ResetScenario(ops).ServeHTTP(rec, req)

	res := rec.Result()
	res.Request = req
	doc := problem(t, res, rec.Body.String(), http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}

func TestResetScenario_AKeyResetsTheInstanceOfThatKeyOnly(t *testing.T) {
	store := newScenarios([4]string{"pay", "checkout", "started", "paid"})
	store.store.SetScenario("checkout", "paid")
	store.store.SetScenario("checkout", "paid", matchscenario.Key("t1"))
	store.store.SetScenario("checkout", "paid", matchscenario.Key("t2"))
	base := serve(t, newStore(t), adminapi.Capabilities{Scenarios: store})

	res, body := send(t, request{method: http.MethodDelete, url: base + "/scenarios/checkout?key=t1"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, "started", store.store.State("checkout", matchscenario.Key("t1")))
	require.Equal(t, "paid", store.store.State("checkout", matchscenario.Key("t2")))

	res, body = send(t, request{method: http.MethodDelete, url: base + "/scenarios/checkout?key="})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, "started", store.store.State("checkout"), "an empty key is the shared instance")
	require.Equal(t, "paid", store.store.State("checkout", matchscenario.Key("t2")))

	res, body = send(t, request{method: http.MethodDelete, url: base + "/scenarios/checkout"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, "started", store.store.State("checkout", matchscenario.Key("t2")), "without a key, every instance")
}
