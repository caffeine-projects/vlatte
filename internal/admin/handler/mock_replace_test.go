package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceMock(t *testing.T) {
	// PUT swaps a mock for the one sent, keeping its id: the old one is closed, and the new one reads back.
	old := newFake("hello")
	store := newStore(t, old)
	url := serve(t, store)

	res, body := send(t, request{method: http.MethodPut, url: url + "/mocks/hello", contentType: "application/yaml", body: "id: hello\nname: Hi\n"})

	require.Equal(t, http.StatusOK, res.StatusCode, body)
	doc := conforms(t, res, body, "MockSummary")
	require.Equal(t, "Hi", doc["name"])
	require.Equal(t, int64(1), old.closed.Load(), "the replaced mock is closed")

	_, body = send(t, request{method: http.MethodGet, url: url + "/mocks/hello"})
	require.JSONEq(t, `{"id": "hello", "name": "Hi"}`, body)
}

func TestReplaceMock_Refused(t *testing.T) {
	// The body's id must be the path's: a body without one would get a new id, and replace the mock with another. A
	// mock built for a refused PUT is closed, and the mock in place stays.
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "another id", path: "/mocks/hello", body: `{"id": "other"}`, wantStatus: http.StatusBadRequest, wantCode: "id_mismatch"},
		{name: "no id", path: "/mocks/hello", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: "id_mismatch"},
		{name: "an unknown mock", path: "/mocks/missing", body: `{"id": "missing"}`, wantStatus: http.StatusNotFound, wantCode: "mock_not_found"},
		{name: "a mock with problems", path: "/mocks/hello", body: `{"id": "hello", "bad": 1}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := newFake("hello")
			store := newStore(t, old)
			b := &fakeBuilder{}
			url := serveWith(t, store, b)

			res, body := send(t, request{method: http.MethodPut, url: url + tt.path, body: tt.body})

			problem(t, res, body, tt.wantStatus, tt.wantCode)

			stored, err := store.Pick(t.Context(), "hello")
			require.NoError(t, err)
			require.Same(t, old, stored, "the mock in place stays")
			require.Zero(t, old.closed.Load())

			for _, m := range b.built {
				require.Equal(t, int64(1), m.closed.Load(), "the mock built is closed")
			}
		})
	}
}
