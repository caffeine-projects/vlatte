package test

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/stretchr/testify/require"
)

func TestRepeat(t *testing.T) {
	m := xhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(xhttp.Get(filter.URLPath("/test")).
		Times(3).
		Reply(xhttp.OK()))

	res, err := http.Get(m.URL() + "/test")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)

	res, err = http.Get(m.URL() + "/test")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)

	res, err = http.Get(m.URL() + "/test")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)

	res, err = http.Get(m.URL() + "/test")
	require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)
}

func TestRepeat_Once(t *testing.T) {
	m := xhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(xhttp.Get(filter.URLPath("/test")).
		Once().
		Reply(xhttp.OK()))

	res, _ := http.Get(m.URL() + "/test")
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = http.Get(m.URL() + "/test")
	require.NoError(t, res.Body.Close())
	require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)
}

func TestRepeat_FileSetup(t *testing.T) {
	m := xhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(xhttp.FromFile("testdata/repeat/1_repeat.yaml"))

	httpClient := &http.Client{}

	for i := range 5 {
		res, err := httpClient.Get(m.URL("test"))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode, i)
	}

	res, err := httpClient.Get(m.URL("/test"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, xhttp.StatusNoMatch, res.StatusCode)
}
