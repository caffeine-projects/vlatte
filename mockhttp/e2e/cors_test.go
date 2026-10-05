package e2e

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/stretchr/testify/require"
)

func TestCORS(t *testing.T) {
	client := &http.Client{}
	m := mockhttp.MustNew(mockhttp.Setup().CORS())
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.Get(match.URLPath("/test")).
		Reply(mockhttp.OK()))

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
