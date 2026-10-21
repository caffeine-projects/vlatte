package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetMock(t *testing.T) {
	// A mock reads back as the mock file that describes it, in the format asked for, so it can be edited and sent
	// back.
	m := newFake("hello")
	m.doc = map[string]any{"id": "hello", "response": map[string]any{"status": int64(200), "body": "line 1\nline 2"}}

	url := serve(t, newStore(t, m))

	t.Run("json", func(t *testing.T) {
		res, body := send(t, request{method: http.MethodGet, url: url + "/mocks/hello"})

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "application/json", res.Header.Get("Content-Type"))
		require.JSONEq(t, `{"id": "hello", "response": {"status": 200, "body": "line 1\nline 2"}}`, body)
	})

	t.Run("yaml", func(t *testing.T) {
		res, body := send(t, request{method: http.MethodGet, url: url + "/mocks/hello", accept: "application/yaml"})

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "application/yaml", res.Header.Get("Content-Type"))
		require.Equal(t, "id: hello\nresponse:\n    body: |-\n        line 1\n        line 2\n    status: 200\n", body)
	})
}

func TestGetMock_Unknown(t *testing.T) {
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks/missing"})

	doc := problem(t, res, body, http.StatusNotFound, "mock_not_found")
	require.Equal(t, `no mock has the id "missing"`, doc["detail"])
}

func TestGetMock_ADescriptionThatCantBeEncoded(t *testing.T) {
	// A description that fails to encode, such as one with a binary body (#42), is a whole problem document: the
	// status was written after part of the body, which then broke off (#49).
	m := newFake("binary")
	m.doc = map[string]any{"id": "binary", "response": map[string]any{"body": "\xff\xfe"}}

	url := serve(t, newStore(t, m))

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks/binary"})

	doc := problem(t, res, body, http.StatusInternalServerError, "describe_failed")
	require.True(t, strings.HasPrefix(doc["detail"].(string), `the mock "binary" can't be described: `), doc["detail"])
}
