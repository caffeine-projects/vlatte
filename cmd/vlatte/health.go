package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// _healthTimeout bounds the health check, below the 30s Docker waits for a HEALTHCHECK by default.
const _healthTimeout = 5 * time.Second

// healthCmd checks that a running vlatte serves, for a container's health check, which needs a command: the vlatte
// image has no shell or HTTP client. It sets code to 0 when it does, and to 1 otherwise.
func healthCmd(code *int) *cobra.Command {
	return &cobra.Command{
		Use:   "health [url]",
		Short: "check that a running server serves, as a container health check",
		Long: `Checks that the server of the vlatte image serves, and exits with 0 when it does, and 1 otherwise, as a container
health check expects: the readiness probe of its admin API, GET /readyz on port 8081, must answer 200. Where nothing
listens on that port, as with --admin=false or another --admin-addr, its mock server, on port 8080 or 8443, must
accept connections. It reaches them on VLATTE_DOCKER_HOST, or 127.0.0.1 when that names every interface.
With a URL, it checks that a GET of it answers 200 instead.`,
		Example: "  vlatte health\n  vlatte health http://127.0.0.1:9000/readyz\n",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), _healthTimeout)
			defer cancel()

			var err error
			if len(args) > 0 {
				err = probe(ctx, args[0])
			} else {
				host := healthHost()
				err = checkServer(ctx, "http://"+net.JoinHostPort(host, _dockerAdminPort)+"/readyz",
					net.JoinHostPort(host, _dockerHTTPPort), net.JoinHostPort(host, _dockerHTTPSPort))
			}

			if err != nil {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err)
				*code = 1
			}

			return nil
		},
	}
}

// healthHost is the host the health check reaches the server of the vlatte image on.
func healthHost() string {
	host := strings.TrimSuffix(dockerHost(), ":")
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		return "127.0.0.1"
	}

	return host
}

// checkServer checks the readiness probe at adminURL, or, when nothing listens there, that one of mockAddrs accepts
// connections. Connecting doesn't send the mock server a request, which its log and journal would keep.
func checkServer(ctx context.Context, adminURL string, mockAddrs ...string) error {
	err := probe(ctx, adminURL)
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return err
	}

	var d net.Dialer
	for _, addr := range mockAddrs {
		conn, dialErr := d.DialContext(ctx, "tcp", addr)
		if dialErr == nil {
			_ = conn.Close()
			return nil
		}

		err = errors.Join(err, fmt.Errorf("health: %w", dialErr))
	}

	return err
}

// probe checks that a GET of url answers 200.
func probe(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}

	// A transport without a proxy asks the server directly: a proxy the environment names would answer for it.
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}

	_ = res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health: GET %s: %s", url, res.Status)
	}

	return nil
}
