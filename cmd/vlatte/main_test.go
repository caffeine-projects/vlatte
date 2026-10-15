package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		maxAttempts := 5
		attempts := 0
		success := false

		for range maxAttempts {
			res, err := http.Get("http://localhost:8080")

			if err == nil && res.StatusCode == sc {
				res.Body.Close()
				success = true
				break
			}

			if attempts == maxAttempts {
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

func TestRun_SubcommandHelp(t *testing.T) {
	// The settings list belongs to the server command: subcommands keep cobra's help.
	for _, args := range [][]string{{"help", "version"}, {"version", "--help"}} {
		var out bytes.Buffer
		require.Equal(t, 0, run(context.Background(), args, &out, io.Discard), args)

		help := out.String()
		require.Contains(t, help, "vlatte version [flags]", args)
		require.NotContains(t, help, "--log-level", args)
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

	// Flags without allowed values, such as --config, leave their values to the shell's file completion.
	var paths bytes.Buffer
	require.Equal(t, 0, run(context.Background(), []string{"__complete", "--config", ""}, &paths, io.Discard))
	require.Equal(t, ":0\n", paths.String(), "no completions, and directive 0: ShellCompDirectiveDefault")

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
		{
			name: "error status code out of range",
			args: []string{"--error-status-code", "99"},
			want: []string{`error_status_code="99": expected an HTTP status code from 100 to 599`},
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

// writeWebhookMock writes a mock file for GET /orders, which answers 202 and sends a webhook to url, into a new
// directory, and returns the pattern of its mock files.
func writeWebhookMock(t *testing.T, url string) string {
	t.Helper()

	dir := t.TempDir()
	mock := fmt.Sprintf(`
request: {method: GET, path: /orders}
response: {status: 202}
on_complete:
  - name: webhook
    parameters: {method: POST, url: %q, body: paid}
`, url)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "orders.mock.yaml"), []byte(mock), 0o600))

	return filepath.Join(dir, "*.yaml")
}

// getOrders gets /orders from the CLI listening on addr, once it is up.
func getOrders(t *testing.T, addr string) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Get("http://" + addr + "/orders")
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, http.StatusAccepted, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestCLI_ServesMockFilesWithWebhooks(t *testing.T) {
	// #16: the CLI registered no on_complete listener, so a mock file with a webhook failed to load, and the CLI exited.
	withoutDockerHost(t)

	got := make(chan string, 4)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.Method + " " + r.URL.Path + " " + string(b)
	}))
	t.Cleanup(target.Close)

	addr := randomAddr(t)
	stop := start(t, "--addr", addr, "--mock-files", writeWebhookMock(t, target.URL+"/hook"), "--log-level", "disabled")
	getOrders(t, addr)

	select {
	case hook := <-got:
		require.Equal(t, "POST /hook paid", hook)
	default:
		t.Fatal("the response came before the webhook")
	}

	require.Equal(t, 0, stop(), "a stopped server exits cleanly")
}

func TestCLI_WebhookTimeout(t *testing.T) {
	// webhook.timeout bounds the webhooks of the CLI, which wait 10s for an answer by default.
	withoutDockerHost(t)

	canceled := make(chan struct{}, 4)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// net/http sees the client leave once the body is read: then the request's context ends.
		_, _ = io.ReadAll(r.Body)
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	t.Cleanup(target.Close)

	addr := randomAddr(t)
	start(t, "--addr", addr, "--mock-files", writeWebhookMock(t, target.URL+"/hook"), "--log-level", "disabled",
		"--webhook-timeout", "100ms")
	getOrders(t, addr)

	// The response waits for the webhook, which webhook.timeout ends: the 10s default would hold it past the client's
	// timeout.
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get("http://" + addr + "/orders")
	require.NoError(t, err, "the response waited past the webhook's 100ms timeout")
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("the webhook waited past its 100ms timeout")
	}
}

func TestCLI_RecordsThenPlaysBack(t *testing.T) {
	// --record --record-target records the target's answers until the CLI stops, into testdata/_mocks_recorded. A
	// later run serves them, without the target: the default mock files include them.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"page":%q}`, r.URL.Query().Get("page"))
	}))
	t.Cleanup(target.Close)

	get := func(c require.TestingT, url string) (int, string) {
		res, err := http.Get(url)
		require.NoError(c, err)

		defer res.Body.Close()

		b, err := io.ReadAll(res.Body)
		require.NoError(c, err)

		return res.StatusCode, string(b)
	}

	addr := randomAddr(t)
	stop := start(t, "--addr", addr, "--record", "--record-target", target.URL, "--log-level", "disabled")

	// Until the recording starts, no mock answers.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Get("http://" + addr + "/items?page=2")
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, http.StatusOK, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)

	require.Equal(t, 0, stop(), "a stopped server exits cleanly")

	files, err := filepath.Glob(filepath.Join("testdata", "_mocks_recorded", "*.mock.json"))
	require.NoError(t, err)
	require.Len(t, files, 1, "the CLI wrote the recording when it stopped")

	target.Close()

	addr = randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled")

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Get("http://" + addr)
		if assert.NoError(c, err) {
			_ = res.Body.Close()
		}
	}, 10*time.Second, 100*time.Millisecond)

	status, body := get(t, "http://"+addr+"/items?page=2")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"page":"2"}`, body)

	status, _ = get(t, "http://"+addr+"/items?page=3")
	require.Equal(t, mockhttp.StatusNoMatch, status, "a request the recording didn't take")
}

func TestCLI_ARecordingThatFailsFailsTheRun(t *testing.T) {
	// A call the recording couldn't record, here a status no mock file holds, makes the run fail when it stops.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(999) }))
	t.Cleanup(target.Close)

	addr := randomAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer

	done := make(chan int, 1)
	go func() {
		done <- run(ctx, []string{"--addr", addr, "--record", "--record-target", target.URL, "--log-level", "disabled"}, io.Discard, &stderr)
	}()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Get("http://" + addr + "/profile")
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, 999, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)

	cancel()
	require.Equal(t, 1, <-done)
	require.Contains(t, stderr.String(), "status 999")
}
