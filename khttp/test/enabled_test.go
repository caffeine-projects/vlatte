package test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/khttp"
)

func TestEnabledDisabledMocks(t *testing.T) {
	httpClient := &http.Client{}
	m := khttp.NewAPI(khttp.Setup().MockFilePatterns("testdata/enabled/*.yaml")).CloseWithT(t)
	m.MustStart()

	testCases := []struct {
		path   string
		status int
	}{
		{"/test", 200},
		{"/hello", 200},
		{"/dev", khttp.StatusNoMatch},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			res, err := httpClient.Get(m.URL(tc.path))

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}
