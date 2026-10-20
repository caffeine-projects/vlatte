package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListMocks(t *testing.T) {
	// The list tells the state of every mock, disabled ones too, in the order mocks match: a client sees which mocks
	// requests reached, without a journal of requests.
	first, second := newFake("first"), newFake("second")
	first.origin = "file mocks/first.yaml"
	first.hits.Store(3)
	second.priority = 1
	second.Disable()

	url := serve(t, newStore(t, second, first))

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	doc := conforms(t, res, body, "MockList")
	require.Equal(t, []any{
		map[string]any{"id": "first", "priority": int64(0), "enabled": true, "hits": int64(3), "origin": "file mocks/first.yaml"},
		map[string]any{"id": "second", "priority": int64(1), "enabled": false, "hits": int64(0)},
	}, doc["mocks"])
}

func TestListMocks_None(t *testing.T) {
	// No mocks is an empty list, not null: clients range over it.
	url := serve(t, newStore(t))

	_, body := send(t, request{method: http.MethodGet, url: url + "/mocks"})

	require.JSONEq(t, `{"mocks": []}`, body)
}
