package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
)

func TestResetScenarios_OnOperationsThatKeepNoScenarios(t *testing.T) {
	// The handler, mounted apart from the admin API's routes on a server without scenarios, answers what its route
	// would: a 501 problem, never a 204 that tells a test its scenarios started over.
	ops := handler.NewOperations(newStore(t), nil, handler.Capabilities{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/scenarios", nil)
	rec := httptest.NewRecorder()

	handler.ResetScenarios(ops).ServeHTTP(rec, req)

	res := rec.Result()
	res.Request = req
	doc := problem(t, res, rec.Body.String(), http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}
