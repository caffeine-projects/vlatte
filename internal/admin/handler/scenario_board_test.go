package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenarioBoard_ServesThePageEvenWithoutScenarios(t *testing.T) {
	// The page loads on every server. On one that keeps no scenarios, it shows the detail of the 501 of GET /scenarios.
	base := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: base + "/scenario-board"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"))

	// Its URLs are relative, so it works wherever the admin API is mounted.
	require.Contains(t, body, "call('GET', 'scenarios')")
	require.Contains(t, body, "new EventSource('events')")

	res, body = send(t, request{method: http.MethodGet, url: base + "/scenarios"})
	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}
