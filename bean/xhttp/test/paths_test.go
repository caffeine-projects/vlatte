package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	khttp "github.com/caffeine-projects/vlatte/bean/xhttp"
)

func TestSimilarRequestURLPaths(t *testing.T) {
	m := khttp.NewAPI()
	m.MustStart()

	defer m.Close()

	scope := m.MustMock(
		khttp.Getf("/customers").Reply(khttp.OK()),
		khttp.Getf("/customers/100").Reply(khttp.Accepted()),
		khttp.Getf("/customers/100/orders").Reply(khttp.Unauthorized()),
		khttp.Getf("/customers/100/orders/BR-500").Reply(khttp.BadRequest()),
		khttp.Getf("/customers/100/orders/BR-500/items").Reply(khttp.InternalServerError()))

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
	require.Equal(t, khttp.StatusNoMatch, res.StatusCode)

	scope.AssertNumberOfCalls(t, 5)
}
