package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveMock(t *testing.T) {
	// A removed mock is closed, and gone: removing it again is a 404, not a success that removed nothing.
	m, other := newFake("hello"), newFake("other")
	store := newStore(t, m, other)
	url := serve(t, store)

	res, body := send(t, request{method: http.MethodDelete, url: url + "/mocks/hello"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, []string{"other"}, ids(t, store))
	require.Equal(t, int64(1), m.closed.Load())

	res, body = send(t, request{method: http.MethodDelete, url: url + "/mocks/hello"})
	problem(t, res, body, http.StatusNotFound, "mock_not_found")
}
