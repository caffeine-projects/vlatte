package handler_test

import (
	"net/http"
	"strings"
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

func TestGetMockTool_ADescriptionThatCantBeEncoded(t *testing.T) {
	// A description that fails to encode, such as one with a binary body, is the problem document the route answers.
	m := newFake("binary")
	m.doc = map[string]any{"id": "binary", "response": map[string]any{"body": "\xff\xfe"}}
	cs := connect(t, newStore(t, m))

	doc := callFails(t, cs, "get_mock", map[string]any{"id": "binary"}, http.StatusInternalServerError, "describe_failed")
	require.True(t, strings.HasPrefix(doc["detail"].(string), `the mock "binary" can't be described: `), doc["detail"])
}
