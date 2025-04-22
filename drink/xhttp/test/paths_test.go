package test

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/drink/xhttp"
	"github.com/caffeine-projects/vlatte/sip"
	"github.com/stretchr/testify/require"
)

func TestSimilarRequestURLPaths(t *testing.T) {
	m := xhttp.New()
	m.MustStart()

	defer m.Close()

	scope := m.MustBrew(
		xhttp.Getf("/customers").Reply(xhttp.OK()),
		xhttp.Getf("/customers/100").Reply(xhttp.Accepted()),
		xhttp.Getf("/customers/100/orders").Reply(xhttp.Unauthorized()),
		xhttp.Getf("/customers/100/orders/BR-500").Reply(xhttp.BadRequest()),
		xhttp.Getf("/customers/100/orders/BR-500/items").Reply(xhttp.InternalServerError()))

	res, err := http.DefaultClient.Get(m.URL() + "/customers")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100/orders")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100/orders/BR-500")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/100/orders/BR-500/items")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)

	res, err = http.DefaultClient.Get(m.URL() + "/customers/orders")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)

	sip.Times(t, scope, 5)
}
