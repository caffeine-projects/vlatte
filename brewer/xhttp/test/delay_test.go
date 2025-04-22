package test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	. "github.com/caffeine-projects/vlatte/brewer/xhttp"
	"github.com/caffeine-projects/vlatte/filter"
)

func TestResponseDelay(t *testing.T) {
	m := New()
	m.MustStart()

	defer m.Close()

	start := time.Now()
	delay := 250 * time.Millisecond

	scoped := m.MustBrew(Get(filter.URLPath("/test")).
		Delay(vlatte.Latency(delay)).
		Reply(OK()))

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
	m := New().CloseWithT(t)
	m.MustStart()

	testCases := []struct {
		name     string
		filename string
		path     string
		delay    time.Duration
	}{
		{"duration string format", "testdata/delay/1_delay_duration_format.yaml", "/duration_string_format", 1 * time.Second},
		{"duration number", "testdata/delay/2_delay_number.yaml", "/duration_number", 1 * time.Second},
		{"duration float", "testdata/delay/3_delay_float.yaml", "/duration_float", time.Duration(1000.50 * float64(time.Millisecond))},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			scoped := m.MustBrew(FromFile(tc.filename))

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
