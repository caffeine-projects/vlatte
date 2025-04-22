package test

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/drink/xhttp"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/stretchr/testify/require"
)

func TestCORS(t *testing.T) {
	client := &http.Client{}
	m := xhttp.New(xhttp.Setup().CORS())
	m.MustStart()

	defer m.Close()

	m.MustBrew(xhttp.Get(filter.URLPath("/test")).
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
