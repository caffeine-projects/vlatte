package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
)

func TestHealth(t *testing.T) {
	// Clients wait on GET /health to know the admin API serves. With the admin API on an address of its own, it also
	// tells where the mock server serves its mocks, which the recorder page shows.
	url := serve(t, newStore(t), admin.Info("http", func() string { return "http://127.0.0.1:8080" }))

	res, body := send(t, request{method: http.MethodGet, url: url + "/health"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	doc := conforms(t, res, body, "Health")
	require.Equal(t, map[string]any{"status": "ok", "kind": "http", "url": "http://127.0.0.1:8080"}, doc)
}
