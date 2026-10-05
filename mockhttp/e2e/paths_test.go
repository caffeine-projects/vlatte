package e2e

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/stretchr/testify/require"
)

func TestSimilarRequestURLPaths(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	scope := m.MustMock(
		mockhttp.Getf("/customers").Reply(mockhttp.OK()),
		mockhttp.Getf("/customers/100").Reply(mockhttp.Accepted()),
		mockhttp.Getf("/customers/100/orders").Reply(mockhttp.Unauthorized()),
		mockhttp.Getf("/customers/100/orders/BR-500").Reply(mockhttp.BadRequest()),
		mockhttp.Getf("/customers/100/orders/BR-500/items").Reply(mockhttp.InternalServerError()))

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
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

	vlatte.Times(t, scope, 5)
}
