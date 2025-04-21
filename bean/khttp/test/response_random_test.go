package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/bean/khttp"
)

func TestRandom_SetupFromFileWithSeed(t *testing.T) {
	m := khttp.NewAPI()
	m.MustStart()

	defer m.Close()

	m.MustMock(khttp.FromFile("testdata/response_random/rand_01.yaml"))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}
