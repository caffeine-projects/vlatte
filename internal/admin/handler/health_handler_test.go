package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestHealth(t *testing.T) {
	// Clients wait on GET /health to know the admin API serves. With the admin API on an address of its own, it also
	// tells where the mock server serves its mocks, which the recorder page shows.
	url := serve(t, newStore(t), adminapi.Capabilities{Kind: "http", URL: func() string { return "http://127.0.0.1:8080" }})

	res, body := send(t, request{method: http.MethodGet, url: url + "/health"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	doc := conforms(t, res, body, "Health")
	require.Equal(t, map[string]any{"status": "ok", "kind": "http", "url": "http://127.0.0.1:8080"}, doc)
}

func TestProbes_LiveAndStartedIgnoreTheStore(t *testing.T) {
	// A store that is down makes the pod unready, never restarted: the liveness and startup probes of Kubernetes answer
	// while the admin API serves, whatever the store says.
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), healthErr: errors.New("store: down")}
	url := serve(t, store, adminapi.Capabilities{Kind: "http"})

	for _, path := range []string{"/livez", "/startupz"} {
		res, body := send(t, request{method: http.MethodGet, url: url + path})

		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.Equal(t, map[string]any{"status": "ok", "kind": "http"}, conforms(t, res, body, "Health"), path)
	}
}

func TestReady_AnswersOnceTheStoreServes(t *testing.T) {
	// Kubernetes sends traffic to a pod whose store can serve its mocks.
	url := serve(t, newStore(t), adminapi.Capabilities{Kind: "http"})

	res, body := send(t, request{method: http.MethodGet, url: url + "/readyz"})

	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"status": "ok", "kind": "http"}, conforms(t, res, body, "Health"))
}

func TestReady_AStoreThatIsDownIsUnavailable(t *testing.T) {
	// A store whose database is down can't serve the mocks: Kubernetes takes the pod out of service until it is back,
	// and the problem tells why.
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), healthErr: errors.New("store: down")}
	url := serve(t, store)

	res, body := send(t, request{method: http.MethodGet, url: url + "/readyz"})

	doc := problem(t, res, body, http.StatusServiceUnavailable, "not_ready")
	require.Equal(t, "the mock store isn't ready: store: down", doc["detail"])
}

func TestProbes_NeedNoCredentials(t *testing.T) {
	// Kubernetes sends no credentials: a probe that needed them would restart the server on a wrong one. The probes
	// tell a stranger only that the server serves, not what or where.
	url := serveAuth(t, handler.Credentials{Token: "tok"}, adminapi.Capabilities{Kind: "http", URL: func() string { return "http://127.0.0.1:8080" }})

	for _, path := range []string{"/livez", "/readyz", "/startupz"} {
		res, body := send(t, request{method: http.MethodGet, url: url + path})

		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.Equal(t, map[string]any{"status": "ok"}, conforms(t, res, body, "Health"), path)

		res, _ = send(t, request{method: http.MethodHead, url: url + path})
		require.Equal(t, http.StatusOK, res.StatusCode, path)
	}

	res, body := send(t, request{method: http.MethodGet, url: url + "/health"})
	problem(t, res, body, http.StatusUnauthorized, "unauthorized")
}

func TestReady_AnOpenProbeHidesWhyTheStoreIsDown(t *testing.T) {
	// The store's error can name its database: a stranger gets the status alone.
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), healthErr: errors.New("store: db.internal:5432 is down")}
	s := start(t, admin.Options{Auth: handler.Credentials{Token: "tok"}}, store, &fakeBuilder{}, adminapi.Capabilities{})

	res, body := send(t, request{method: http.MethodGet, url: s.URL() + "/readyz"})

	doc := problem(t, res, body, http.StatusServiceUnavailable, "not_ready")
	require.Equal(t, "the mock store isn't ready", doc["detail"])
}
