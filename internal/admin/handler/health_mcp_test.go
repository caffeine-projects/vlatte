package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestHealthTool(t *testing.T) {
	// An agent learns what it manages: the kind of the mock server, and where it serves its mocks.
	cs := connect(t, newStore(t), adminapi.Capabilities{Kind: "http", URL: func() string { return "http://127.0.0.1:8080" }})

	doc := conformsAs(t, call(t, cs, "health", nil), "json", "Health")
	require.Equal(t, map[string]any{"status": "ok", "kind": "http", "url": "http://127.0.0.1:8080"}, doc)
}
