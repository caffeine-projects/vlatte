package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveAllMocks(t *testing.T) {
	// Every mock goes, and is closed.
	a, b := newFake("a"), newFake("b")
	store := newStore(t, a, b)
	url := serve(t, store)

	res, body := send(t, request{method: http.MethodDelete, url: url + "/mocks"})

	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Empty(t, ids(t, store))
	require.Equal(t, int64(1), a.closed.Load())
	require.Equal(t, int64(1), b.closed.Load())
}
