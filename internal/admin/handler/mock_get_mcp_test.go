package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetMockTool(t *testing.T) {
	// A mock reads back as the mock file that describes it, which replace_mock takes back.
	m := newFake("hello")
	m.doc = map[string]any{"id": "hello", "response": map[string]any{"status": int64(200)}}
	cs := connect(t, newStore(t, m))

	require.JSONEq(t, `{"id": "hello", "response": {"status": 200}}`, call(t, cs, "get_mock", map[string]any{"id": "hello"}))

	doc := callFails(t, cs, "get_mock", map[string]any{"id": "missing"}, http.StatusNotFound, "mock_not_found")
	require.Equal(t, `no mock has the id "missing"`, doc["detail"])
}
