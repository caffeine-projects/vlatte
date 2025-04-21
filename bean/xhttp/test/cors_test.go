package test

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/bean/xhttp"
	"github.com/caffeine-projects/vlatte/matcher"
	"github.com/stretchr/testify/require"
)

func TestCORS(t *testing.T) {
	client := &http.Client{}
	m := xhttp.NewAPI(xhttp.Setup().CORS())
	m.MustStart()

	defer m.Close()

	m.MustMock(xhttp.Get(matcher.URLPath("/test")).
		Reply(xhttp.OK()))

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
