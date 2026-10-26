package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveMockTool(t *testing.T) {
	store := newStore(t, newFake("a"), newFake("b"))
	cs := connect(t, store)

	require.Equal(t, "done", call(t, cs, "remove_mock", map[string]any{"id": "a"}))
	require.Equal(t, []string{"b"}, ids(t, store))

	callFails(t, cs, "remove_mock", map[string]any{"id": "a"}, http.StatusNotFound, "mock_not_found")
}
