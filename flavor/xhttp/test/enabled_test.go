package test

import (
	"net/http"
	"testing"

	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/stretchr/testify/require"
)

func TestEnabledDisabledMocks(t *testing.T) {
	httpClient := &http.Client{}
	m := xhttp.MustNew(xhttp.Setup().MockFilePatterns("testdata/enabled/*.yaml"))
	t.Cleanup(func() { m.Close() })
	m.MustStart()

	testCases := []struct {
		path   string
		status int
	}{
		{"/test", 200},
		{"/hello", 200},
		{"/dev", xhttp.StatusNoMatch},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			res, err := httpClient.Get(m.URL(tc.path))

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}
