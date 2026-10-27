package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestScenarios_NotSupported(t *testing.T) {
	base := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: base + "/scenarios"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}

func TestListScenarios_OnOperationsThatKeepNoScenarios(t *testing.T) {
	// The handler, mounted apart from the admin API's routes on a server without scenarios, answers what its route
	// would: a 501 problem, never an empty list that reads as no scenario started.
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil)})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/scenarios", nil)
	rec := httptest.NewRecorder()

	handler.ListScenarios(ops).ServeHTTP(rec, req)

	res := rec.Result()
	res.Request = req
	doc := problem(t, res, rec.Body.String(), http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}
