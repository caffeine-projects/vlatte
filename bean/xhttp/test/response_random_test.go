package test

import (
	"net/http"
	"testing"

	khttp "github.com/caffeine-projects/vlatte/bean/xhttp"
	"github.com/stretchr/testify/require"
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
