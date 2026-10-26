package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceMockTool(t *testing.T) {
	// A mock without an id takes the one it replaces: an agent edits what get_mock gave and sends it back.
	cs := connect(t, newStore(t, newFake("hello")))

	doc := conformsAs(t, call(t, cs, "replace_mock", map[string]any{"id": "hello", "mock": map[string]any{"name": "Hi"}}), "json", "MockSummary")
	require.Equal(t, "hello", doc["id"])
	require.Equal(t, "Hi", doc["name"])
	require.JSONEq(t, `{"id": "hello", "name": "Hi"}`, call(t, cs, "get_mock", map[string]any{"id": "hello"}))
}

func TestReplaceMockTool_Refused(t *testing.T) {
	cs := connect(t, newStore(t, newFake("hello")))

	callFails(t, cs, "replace_mock", map[string]any{"id": "hello", "mock": map[string]any{"id": "other"}}, http.StatusBadRequest, "id_mismatch")
	callFails(t, cs, "replace_mock", map[string]any{"id": "missing", "mock": map[string]any{"name": "x"}}, http.StatusNotFound, "mock_not_found")
}
