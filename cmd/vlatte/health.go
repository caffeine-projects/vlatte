package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// _healthTimeout bounds the health check, below the 30s Docker waits for a HEALTHCHECK by default.
const _healthTimeout = 5 * time.Second

// healthCmd checks that a running vlatte is ready, for a container's health check, which needs a command: the vlatte
// image has no shell or HTTP client. It sets code to 0 when the admin API's readiness probe answers 200, and to 1
// otherwise.
func healthCmd(code *int) *cobra.Command {
	return &cobra.Command{
		Use:   "health [url]",
		Short: "check that a running server is ready, through its admin API",
		Long: `Asks the readiness probe of a running server's admin API, GET /readyz, and exits with 0 when it answers 200, and 1
otherwise, as a container health check expects. The URL defaults to the probe of the vlatte image: port 8081 of
VLATTE_DOCKER_HOST, or of 127.0.0.1 when that names every interface.`,
		Example: "  vlatte health\n  vlatte health http://127.0.0.1:9000/readyz\n",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := healthURL()
			if len(args) > 0 {
				url = args[0]
			}

			*code = checkHealth(cmd.Context(), url, cmd.ErrOrStderr())

			return nil
		},
	}
}

// healthURL is the readiness probe of the admin API in the vlatte image.
func healthURL() string {
	host := strings.TrimSuffix(dockerHost(), ":")
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}

	return "http://" + net.JoinHostPort(host, _dockerAdminPort) + "/readyz"
}

// checkHealth asks url, writing why to stderr when it fails, and returns the exit code.
func checkHealth(ctx context.Context, url string, stderr io.Writer) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "health: %v\n", err)
		return 1
	}

	// A transport without a proxy asks the server directly: a proxy the environment names would answer for it.
	client := &http.Client{Transport: &http.Transport{}, Timeout: _healthTimeout}
	defer client.CloseIdleConnections()

	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "health: %v\n", err)
		return 1
	}

	_ = res.Body.Close()

	if res.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(stderr, "health: GET %s: %s\n", url, res.Status)
		return 1
	}

	return 0
}
