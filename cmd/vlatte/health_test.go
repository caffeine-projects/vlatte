package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHealth_IsHealthyWhileTheServerRuns(t *testing.T) {
	// A container's health check runs the command against the server it runs beside: 0 while that serves, 1 once it
	// stopped, so the container is reported unhealthy.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	adminURL, stop := startAdmin(t, "--addr", randomAddr(t), "--log-level", "disabled")

	require.Eventually(t, func() bool {
		return run(t.Context(), []string{"health", adminURL + "/readyz"}, nil, io.Discard, io.Discard) == 0
	}, 5*time.Second, 10*time.Millisecond)

	require.Equal(t, 0, stop())

	var errOut bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"health", adminURL + "/readyz"}, nil, io.Discard, &errOut))
	require.Contains(t, errOut.String(), "connection refused")
}

func TestHealth_IsUnhealthyWhenTheServerIsNotReady(t *testing.T) {
	// /readyz answers 503 when the mock store can't serve its mocks: the server runs, but can't do its job.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	var errOut bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"health", srv.URL + "/readyz"}, nil, io.Discard, &errOut))
	require.Equal(t, "health: GET "+srv.URL+"/readyz: 503 Service Unavailable\n", errOut.String())
}

func TestHealth_TakesOneURLAtMost(t *testing.T) {
	var errOut bytes.Buffer
	require.Equal(t, 2, run(t.Context(), []string{"health", "http://a", "http://b"}, nil, io.Discard, &errOut))
	require.Contains(t, errOut.String(), "accepts at most 1 arg(s), received 2")
}

func TestHealthURL(t *testing.T) {
	// Without a URL, the command probes the admin API of the vlatte image, which listens on port 8081 of
	// VLATTE_DOCKER_HOST: through the loopback interface when that names every interface.
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "every interface by default", host: "", want: "http://127.0.0.1:8081/readyz"},
		{name: "every IPv4 interface", host: "0.0.0.0", want: "http://127.0.0.1:8081/readyz"},
		{name: "a host", host: "example.org", want: "http://example.org:8081/readyz"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(_dockerHostEnv, tc.host)
			require.Equal(t, tc.want, healthURL())
		})
	}
}
