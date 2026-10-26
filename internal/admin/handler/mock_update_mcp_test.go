package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateMockTool(t *testing.T) {
	// A disabled mock matches no request: an agent turns a mock off for a while without losing it.
	m := newFake("hello")
	cs := connect(t, newStore(t, m))

	doc := conformsAs(t, call(t, cs, "update_mock", map[string]any{"id": "hello", "enabled": false}), "json", "MockSummary")
	require.Equal(t, false, doc["enabled"])
	require.False(t, m.Enabled())

	callFails(t, cs, "update_mock", map[string]any{"id": "missing", "enabled": false}, http.StatusNotFound, "mock_not_found")
}
