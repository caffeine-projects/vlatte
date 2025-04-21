package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	khttp "github.com/caffeine-projects/vlatte/bean/xhttp"
	"github.com/caffeine-projects/vlatte/matcher"
)

func TestRepeat(t *testing.T) {
	m := khttp.NewAPI()
	m.MustStart()

	defer m.Close()

	m.MustMock(khttp.Get(matcher.URLPath("/test")).
		Times(3).
		Reply(khttp.OK()))

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
	require.Equal(t, khttp.StatusNoMatch, res.StatusCode)
	require.NoError(t, res.Body.Close())
	require.NoError(t, err)
}

func TestRepeat_Once(t *testing.T) {
	m := khttp.NewAPI()
	m.MustStart()

	defer m.Close()

	m.MustMock(khttp.Get(matcher.URLPath("/test")).
		Once().
		Reply(khttp.OK()))

	res, _ := http.Get(m.URL() + "/test")
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = http.Get(m.URL() + "/test")
	require.NoError(t, res.Body.Close())
	require.Equal(t, khttp.StatusNoMatch, res.StatusCode)
}

func TestRepeat_FileSetup(t *testing.T) {
	m := khttp.NewAPI().CloseWithT(t)
	m.MustStart()
	m.MustMock(khttp.FromFile("testdata/repeat/1_repeat.yaml"))

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
	require.Equal(t, khttp.StatusNoMatch, res.StatusCode)
}
