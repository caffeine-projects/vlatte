package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateMock(t *testing.T) {
	// A disabled mock matches no request: a client turns a mock off for a while without losing it.
	m := newFake("hello")
	url := serve(t, newStore(t, m))

	res, body := send(t, request{method: http.MethodPatch, url: url + "/mocks/hello", body: `{"enabled": false}`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, false, conforms(t, res, body, "MockSummary")["enabled"])
	require.False(t, m.Enabled())

	res, body = send(t, request{method: http.MethodPatch, url: url + "/mocks/hello", contentType: "application/yaml", body: "enabled: true\n"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.True(t, m.Enabled())

	res, body = send(t, request{method: http.MethodPatch, url: url + "/mocks/hello", body: `{}`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.True(t, m.Enabled(), "an empty change changes nothing")
}

func TestUpdateMock_Refused(t *testing.T) {
	// Only a mock's state changes this way: a key it doesn't know is a problem, not ignored.
	m := newFake("hello")
	url := serve(t, newStore(t, m))

	res, body := send(t, request{method: http.MethodPatch, url: url + "/mocks/hello", body: `{"enabled": false, "priority": 2}`})
	doc := problem(t, res, body, http.StatusBadRequest, "invalid_body")
	require.Len(t, doc["problems"], 1)
	require.True(t, m.Enabled(), "nothing changed")

	res, body = send(t, request{method: http.MethodPatch, url: url + "/mocks/missing", body: `{"enabled": false}`})
	problem(t, res, body, http.StatusNotFound, "mock_not_found")
}
