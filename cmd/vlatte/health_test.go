package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
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
	srv := notReady(t)

	var errOut bytes.Buffer
	require.Equal(t, 1, run(t.Context(), []string{"health", srv.URL + "/readyz"}, nil, io.Discard, &errOut))
	require.Equal(t, "health: GET "+srv.URL+"/readyz: 503 Service Unavailable\n", errOut.String())
}

func TestHealth_TakesOneURLAtMost(t *testing.T) {
	var errOut bytes.Buffer
	require.Equal(t, 2, run(t.Context(), []string{"health", "http://a", "http://b"}, nil, io.Discard, &errOut))
	require.Contains(t, errOut.String(), "accepts at most 1 arg(s), received 2")
}

func TestCheckServer(t *testing.T) {
	// The image's health check works whatever the server's settings: its admin API answers for it, and where the
	// admin API is off or elsewhere, its mock server, whose port no setting changes, does.
	ready := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(ready.Close)

	mocks := listening(t)

	t.Run("the admin API is ready", func(t *testing.T) {
		require.NoError(t, checkServer(t.Context(), ready.URL+"/readyz", randomAddr(t)))
	})

	t.Run("the admin API is not ready", func(t *testing.T) {
		// The admin API answered: the mock server accepting connections doesn't make a server that can't serve its
		// mocks healthy.
		srv := notReady(t)
		require.EqualError(t, checkServer(t.Context(), srv.URL+"/readyz", mocks),
			"health: GET "+srv.URL+"/readyz: 503 Service Unavailable")
	})

	t.Run("no admin API, the mock server serves", func(t *testing.T) {
		require.NoError(t, checkServer(t.Context(), "http://"+randomAddr(t)+"/readyz", randomAddr(t), mocks))
	})

	t.Run("nothing serves", func(t *testing.T) {
		err := checkServer(t.Context(), "http://"+randomAddr(t)+"/readyz", randomAddr(t))
		require.ErrorIs(t, err, syscall.ECONNREFUSED)
	})
}

func TestHealthHost(t *testing.T) {
	// The health check reaches the image's server on VLATTE_DOCKER_HOST: through the loopback interface when that
	// names every interface.
	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "every interface by default", host: "", want: "127.0.0.1"},
		{name: "every interface", host: "0.0.0.0", want: "127.0.0.1"},
		{name: "a host", host: "example.org", want: "example.org"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(_dockerHostEnv, tc.host)
			require.Equal(t, tc.want, healthHost())
		})
	}
}

// notReady returns a server whose readiness probe answers 503.
func notReady(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// listening returns the address of a listener that accepts connections until the test ends.
func listening(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	return l.Addr().String()
}
