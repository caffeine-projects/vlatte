package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenarios_NotSupported(t *testing.T) {
	base := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: base + "/scenarios"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep scenarios", doc["detail"])
}
