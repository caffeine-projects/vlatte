package test

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/brewer/xhttp"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/stretchr/testify/require"
)

func TestRepeat(t *testing.T) {
	m := xhttp.NewAPI()
	m.MustStart()

	defer m.Close()

	m.MustBrew(xhttp.Get(filter.URLPath("/test")).
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
	m := xhttp.NewAPI()
	m.MustStart()

	defer m.Close()

	m.MustBrew(xhttp.Get(filter.URLPath("/test")).
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
	m := xhttp.NewAPI().CloseWithT(t)
	m.MustStart()
	m.MustBrew(xhttp.FromFile("testdata/repeat/1_repeat.yaml"))

	httpClient := &http.Client{}

	for i := 0; i < 5; i++ {
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
