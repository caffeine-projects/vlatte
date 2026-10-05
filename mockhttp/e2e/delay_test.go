package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestResponseDelay(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	start := time.Now()
	delay := 250 * time.Millisecond

	scoped := m.MustMock(mockhttp.Get(match.URLPath("/test")).
		Delay(delay).
		Reply(mockhttp.OK()))

	res, err := http.Get(fmt.Sprintf("%s/test", m.URL()))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	elapsed := time.Since(start)

	scoped.AssertCalled(t)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.GreaterOrEqual(t, elapsed, delay)
}

func TestResponseDelay_SetupFromFile(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{}
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()

	testCases := []struct {
		name     string
		filename string
		path     string
		delay    time.Duration
	}{
		{"duration string format", "testdata/delay/1_delay_duration_format.yaml", "/duration_string_format", 100 * time.Millisecond},
		{"duration number", "testdata/delay/2_delay_number.yaml", "/duration_number", 100 * time.Millisecond},
		{"duration float", "testdata/delay/3_delay_float.yaml", "/duration_float", time.Duration(100.50 * float64(time.Millisecond))},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			scoped := m.MustMock(mockhttp.FromFile(tc.filename))

			res, err := httpClient.Get(m.URL(tc.path))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			elapsed := time.Since(start)

			scoped.AssertCalled(t)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.GreaterOrEqual(t, elapsed, tc.delay)
		})
	}
}
