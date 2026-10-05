package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// start runs the CLI with args until the test ends, or until the returned stop is called.
// stop returns the exit code.
func start(t *testing.T, args ...string) (stop func() int) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)

	go func() { done <- run(ctx, args, io.Discard, io.Discard) }()

	code, stopped := 0, false
	stop = func() int {
		if !stopped {
			cancel()
			code, stopped = <-done, true
		}

		return code
	}

	t.Cleanup(func() { stop() })

	return stop
}

// withoutDockerHost unsets VLATTE_DOCKER_HOST for the test, since it replaces the address.
func withoutDockerHost(t *testing.T) {
	t.Setenv(_dockerHostEnv, "")
	require.NoError(t, os.Unsetenv(_dockerHostEnv))
}

func randomAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	return addr
}

func TestCLI(t *testing.T) {
	// Flags configure the server: it listens on --addr and answers unmatched requests with --no-match-status-code.
	withoutDockerHost(t)

	addr := randomAddr(t)
	stop := start(t, "--addr", addr, "--no-match-status-code", "404")

	// Connection errors mean it is not up yet: they are retried, never counted as success.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Get("http://" + addr)
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, http.StatusNotFound, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)

	require.Equal(t, 0, stop(), "a stopped server exits cleanly")
}

func TestCLI_HTTPS(t *testing.T) {
	withoutDockerHost(t)

	addr := randomAddr(t)
	start(t, "--addr", addr, "--https")

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	t.Cleanup(client.CloseIdleConnections)

	// With HTTPS enabled, the CLI must serve over TLS.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := client.Get("https://" + addr)
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, mockhttp.StatusNoMatch, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestDockerCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel() })
	t.Setenv(_dockerHostEnv, "localhost")

	sc := http.StatusTeapot

	go run(ctx, nil, io.Discard, io.Discard)

	request := func() {
		max := 5
		attempts := 0
		success := false

		for range max {
			res, err := http.Get("http://localhost:8080")

			if err == nil && res.StatusCode == sc {
				res.Body.Close()
				success = true
				break
			}

			if attempts == max {
				t.FailNow()
			}

			<-time.After(2 * time.Second)

			attempts++
		}

		if !success {
			t.FailNow()
		}
	}

	request()

	cancel()
}

func TestRun_Help(t *testing.T) {
	// Help must work even when the config file is broken, since it explains how to fix it.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/.vlatterc.yaml", []byte("name: [unclosed\n"), 0o600))
	t.Chdir(dir)

	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		var out bytes.Buffer
		require.Equal(t, 0, run(context.Background(), args, &out, io.Discard), args)

		help := out.String()
		require.Contains(t, help, "vlatte [flags]", args)
		require.Contains(t, help, "completion", args)
		require.Contains(t, help, "--log-level name", args)
		require.Contains(t, help, "env VLATTE_LOG_LEVEL, file key log.level", args)
	}
}

func TestRun_Version(t *testing.T) {
	var out bytes.Buffer

	require.Equal(t, 0, run(context.Background(), []string{"version"}, &out, io.Discard))
	require.Equal(t, version()+"\n", out.String())
}

func TestRun_Completion(t *testing.T) {
	// cobra completes the flags the config args source parses: every setting must be offered.
	complete := func(args ...string) map[string]bool {
		var out bytes.Buffer
		require.Equal(t, 0, run(context.Background(), append([]string{"__complete"}, args...), &out, io.Discard))

		// One completion per line, with an optional tab-separated description, then a ":<directive>" line.
		offered := make(map[string]bool)
		for line := range strings.Lines(out.String()) {
			if strings.HasPrefix(line, ":") {
				break
			}

			name, _, _ := strings.Cut(strings.TrimSpace(line), "\t")
			offered[name] = true
		}

		return offered
	}

	all := complete("--")
	for _, f := range mockhttp.ConfigFlags() {
		require.True(t, all["--"+f.Name], "--%s is not offered", f.Name)
	}

	require.True(t, complete("--log-")["--log-level"])
	require.True(t, complete("--log-level", "")["info"], "log levels complete by name")
	require.True(t, complete("--log-level=in")["--log-level=info"])
	require.Equal(t, map[string]bool{"debug": true}, complete("--log-level", "de"), "only matching values")

	var script bytes.Buffer
	require.Equal(t, 0, run(context.Background(), []string{"completion", "zsh"}, &script, io.Discard))
	require.Contains(t, script.String(), "#compdef vlatte")
}

func TestRun_UsageErrors(t *testing.T) {
	withoutDockerHost(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "unknown flag",
			args: []string{"--log-levl=1"},
			want: []string{"flag --log-levl: unknown_key: --log-levl is not a known flag; did you mean --log-level?", _helpHint},
		},
		{
			name: "mistyped command",
			args: []string{"versoin"},
			want: []string{`unknown command "versoin" for "vlatte"`, "Did you mean this?\n\tversion", _helpHint},
		},
		{
			name: "go-style flag value after a space",
			args: []string{"-no-match-status-code", "99"},
			want: []string{`no_match_status_code="99": expected an HTTP status code from 100 to 599`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			require.Equal(t, 2, run(context.Background(), tc.args, io.Discard, &errOut))

			for _, want := range tc.want {
				require.Contains(t, errOut.String(), want)
			}
		})
	}
}

func TestRun_PortInUse(t *testing.T) {
	withoutDockerHost(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	var errOut bytes.Buffer
	require.Equal(t, 1, run(context.Background(), []string{"--addr", l.Addr().String()}, io.Discard, &errOut))
	require.Contains(t, errOut.String(), "address already in use")
}

func TestRun_AllowsTheDockerHostVariable(t *testing.T) {
	// VLATTE_DOCKER_HOST shares the settings prefix; the env source must not reject it.
	t.Setenv(_dockerHostEnv, "127.0.0.1")

	m, err := mockhttp.New()
	require.NoError(t, err)

	require.NoError(t, (&dockerConfigurer{}).Apply(m.Config()))
	require.Equal(t, "127.0.0.1:8080", m.Config().Addr)
}
