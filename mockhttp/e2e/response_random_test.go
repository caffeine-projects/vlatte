package e2e

import (
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestRandom_SetupFromFileWithSeed(t *testing.T) {
	serve := func() []int {
		m := mockhttp.MustNew()
		m.MustStart()
		defer m.Close()

		m.MustMock(mockhttp.FromFile("testdata/response_random/rand_01.yaml"))

		statuses := make([]int, 0, 20)
		for range 20 {
			res, err := http.Get(m.URL("/test"))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			statuses = append(statuses, res.StatusCode)
		}
		return statuses
	}

	// A seed makes the sequence reproducible across servers, while it still varies between requests.
	first := serve()
	require.Equal(t, first, serve())
	require.True(t, slices.ContainsFunc(first, func(s int) bool { return s != first[0] }), "seeded replies never varied: %v", first)
}
