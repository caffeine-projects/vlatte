package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceMockTool(t *testing.T) {
	// An agent edits what get_mock gave and sends it back: the mock is replaced in one step.
	cs := connect(t, newStore(t, newFake("hello")))

	doc := conformsAs(t, call(t, cs, "replace_mock", map[string]any{"id": "hello", "mock": map[string]any{"id": "hello", "name": "Hi"}}), "json", "MockSummary")
	require.Equal(t, "hello", doc["id"])
	require.Equal(t, "Hi", doc["name"])
	require.JSONEq(t, `{"id": "hello", "name": "Hi"}`, call(t, cs, "get_mock", map[string]any{"id": "hello"}))
}

func TestReplaceMockTool_Refused(t *testing.T) {
	// As for PUT /mocks/{id}, the mock's id must be the one it replaces: one without an id would get a new one.
	cs := connect(t, newStore(t, newFake("hello")))

	callFails(t, cs, "replace_mock", map[string]any{"id": "hello", "mock": map[string]any{"id": "other"}}, http.StatusBadRequest, "id_mismatch")
	callFails(t, cs, "replace_mock", map[string]any{"id": "hello", "mock": map[string]any{"name": "Hi"}}, http.StatusBadRequest, "id_mismatch")
	callFails(t, cs, "replace_mock", map[string]any{"id": "missing", "mock": map[string]any{"id": "missing"}}, http.StatusNotFound, "mock_not_found")
}
