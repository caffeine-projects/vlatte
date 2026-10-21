package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestRepeat(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.Get(match.URLPath("/test")).
		Times(3).
		Reply(mockhttp.OK()))

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
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)
}

func TestRepeat_Once(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.Get(match.URLPath("/test")).
		Once().
		Reply(mockhttp.OK()))

	res, _ := http.Get(m.URL() + "/test")
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = http.Get(m.URL() + "/test")
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestRepeat_FileSetup(t *testing.T) {
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.FromFile("testdata/repeat/1_repeat.yaml"))

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
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}
