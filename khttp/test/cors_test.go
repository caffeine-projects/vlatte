package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/khttp"
	"github.com/caffeine-projects/vlatte/matcher"
)

func TestCORS(t *testing.T) {
	client := &http.Client{}
	m := khttp.NewAPI(khttp.Setup().CORS())
	m.MustStart()

	defer m.Close()

	m.MustMock(khttp.Get(matcher.URLPath("/test")).
		Reply(khttp.OK()))

	corsReq, _ := http.NewRequest(http.MethodOptions, m.URL()+"/test", nil)
	res, err := client.Do(corsReq)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	res, err = client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}
