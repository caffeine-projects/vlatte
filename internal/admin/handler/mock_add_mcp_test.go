package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddMockTool(t *testing.T) {
	// An agent adds a mock as a mock file writes it, and the server serves it from then on.
	store := newStore(t)
	cs := connect(t, store)

	doc := conformsAs(t, call(t, cs, "add_mock", map[string]any{"mock": map[string]any{"id": "hello", "name": "Hi"}}), "json", "MockSummary")
	require.Equal(t, "hello", doc["id"])
	require.Equal(t, "Hi", doc["name"])
	require.Equal(t, []string{"hello"}, ids(t, store))
}

func TestAddMockTool_Refused(t *testing.T) {
	store := newStore(t, newFake("hello"))
	cs := connect(t, store)

	callFails(t, cs, "add_mock", map[string]any{"mock": map[string]any{"id": "hello"}}, http.StatusConflict, "duplicate_id")

	doc := callFails(t, cs, "add_mock", map[string]any{"mock": map[string]any{"bad": 1}}, http.StatusBadRequest, "invalid_body")
	require.Len(t, doc["problems"], 1, "the problems of the mock tell the agent what to fix")
	require.Equal(t, []string{"hello"}, ids(t, store))
}
