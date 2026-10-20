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

func TestListMocks_ANameThatCantBeAnswered(t *testing.T) {
	// A mock named in Go can have a name that isn't UTF-8, which JSON and YAML can't hold: the list is a problem then,
	// never part of an answer.
	m := newFake("hello")
	m.name = "\xff"
	url := serve(t, newStore(t, m))

	for _, accept := range []string{"application/json", "application/yaml"} {
		res, body := send(t, request{method: http.MethodGet, url: url + "/mocks", accept: accept})

		problem(t, res, body, http.StatusInternalServerError, "internal")
	}
}

func TestListMocks_None(t *testing.T) {
	// No mocks is an empty list, not null: clients range over it.
	url := serve(t, newStore(t))

	_, body := send(t, request{method: http.MethodGet, url: url + "/mocks"})

	require.JSONEq(t, `{"mocks": []}`, body)
}
