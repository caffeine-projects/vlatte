package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	json "encoding/json/v2"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
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

func TestMain(m *testing.M) {
	// The command keeps its admin mocks and its journal under the working directory: the tests run in a directory of
	// their own, so none writes into the repository.
	dir, err := os.MkdirTemp("", "vlatte-cli-test-*")
	if err != nil {
		panic(err)
	}

	defer os.RemoveAll(dir)

	if err := os.Chdir(dir); err != nil {
		panic(err)
	}

	m.Run()
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
	// The server outlives the test, which doesn't wait for it to stop: its journal is apart from the next tests'.
	t.Chdir(t.TempDir())

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

// firstAnswer gets url once the CLI listens, and returns the first answer it got: connection errors mean the CLI isn't
// up yet, and are retried. Any answer counts, so the test sees what the first request got.
func firstAnswer(t *testing.T, url string) (int, string) {
	t.Helper()

	var res *http.Response

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		r, err := http.Get(url)
		if assert.NoError(c, err) {
			res = r
		}
	}, 10*time.Second, 100*time.Millisecond)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(body)
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
	// --record --record-target records the target's answers until the CLI stops, into testdata/vlatte/_mocks_recorded. A
	// later run serves them, without the target: the default mock files include them.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"page":%q}`, r.URL.Query().Get("page"))
	}))
	t.Cleanup(target.Close)

	addr := randomAddr(t)
	stop := start(t, "--addr", addr, "--record", "--record-target", target.URL, "--log-level", "disabled")

	// The recording starts before the CLI serves (#54): the first request gets the target's answer.
	status, body := firstAnswer(t, "http://"+addr+"/items?page=2")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"page":"2"}`, body)

	require.Equal(t, 0, stop(), "a stopped server exits cleanly")

	files, err := filepath.Glob(filepath.Join("testdata", "vlatte", "_mocks_recorded", "*.mock.json"))
	require.NoError(t, err)
	require.Len(t, files, 1, "the CLI wrote the recording when it stopped")

	target.Close()

	addr = randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled")

	// The mock files load before the CLI serves (#54): the first request gets the recording.
	status, body = firstAnswer(t, "http://"+addr+"/items?page=2")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"page":"2"}`, body)

	status, _ = firstAnswer(t, "http://"+addr+"/items?page=3")
	require.Equal(t, mockhttp.StatusNoMatch, status, "a request the recording didn't take")
}

func TestCLI_AStartThatFailsLeavesNothingListening(t *testing.T) {
	// #54: a mock file that didn't load failed the run, but the server went on listening, without mocks.
	withoutDockerHost(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.mock.yaml"), []byte("request: {method: GET, path: /x}\nresponse: {stauts: 200}\n"), 0o600))

	addr := randomAddr(t)

	var stderr bytes.Buffer

	code := run(context.Background(), []string{"--addr", addr, "--mock-files", filepath.Join(dir, "*.yaml"), "--log-level", "disabled"}, io.Discard, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "unknown_key: response.stauts")

	l, err := net.Listen("tcp", addr)
	require.NoError(t, err, "the run left nothing listening on its address")
	require.NoError(t, l.Close())
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

func TestCLI_RecordingsItCantServeDontFailTheRun(t *testing.T) {
	// A recording moved to the mock files, then recorded again to refresh it, has the ID of a mock file: the exiting
	// server can't serve it. Every call was recorded, so the run succeeds, and says why the recording isn't served.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	// Each answer of the target is its own, so a test tells the target's from the mock file's.
	var answers atomic.Int64

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, answers.Add(1))
	}))
	t.Cleanup(target.Close)

	// record runs the CLI until the target gave want, which the recording took, and returns the exit code and stderr.
	record := func(want string) (int, string) {
		addr := randomAddr(t)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var stderr bytes.Buffer

		done := make(chan int, 1)
		go func() {
			done <- run(ctx, []string{"--addr", addr, "--record", "--record-target", target.URL, "--log-level", "disabled"}, io.Discard, &stderr)
		}()

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			res, err := http.Get("http://" + addr + "/items")
			if !assert.NoError(c, err) {
				return
			}

			defer res.Body.Close()

			body, err := io.ReadAll(res.Body)
			assert.NoError(c, err)
			assert.Equal(c, want, string(body))
		}, 10*time.Second, 100*time.Millisecond)

		cancel()

		return <-done, stderr.String()
	}

	code, _ := record("1")
	require.Equal(t, 0, code)

	files, err := filepath.Glob(filepath.Join("testdata", "vlatte", "_mocks_recorded", "*.mock.json"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	// JSON is YAML too: the default patterns load the .yaml files of testdata/vlatte/_mocks.
	data, err := os.ReadFile(files[0])
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join("testdata", "vlatte", "_mocks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join("testdata", "vlatte", "_mocks", "items.mock.yaml"), data, 0o600))
	require.NoError(t, os.Remove(files[0]))

	code, stderr := record("2")
	require.Equal(t, 0, code, "every call was recorded")
	require.Contains(t, stderr, "mock id already in use", "why the recording isn't served")
	require.FileExists(t, files[0], "the recording was written again")
}

// lockedBuffer is a bytes.Buffer the CLI writes to while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// startAdmin runs the CLI with args, as start does, and returns the URL of the admin API it prints.
func startAdmin(t *testing.T, args ...string) (string, func() int) {
	t.Helper()

	var stdout lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, args, &stdout, io.Discard) }()

	code, stopped := 0, false
	stop := func() int {
		if !stopped {
			cancel()
			code, stopped = <-done, true
		}

		return code
	}

	t.Cleanup(func() { stop() })

	var adminURL string
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, after, ok := strings.Cut(stdout.String(), "Admin API: ")
		if assert.True(c, ok) {
			adminURL, _, _ = strings.Cut(after, "\n")
		}
	}, 5*time.Second, 10*time.Millisecond)

	return adminURL, stop
}

// postMock adds the mock of doc, a YAML document, through the admin API at adminURL.
func postMock(t *testing.T, adminURL, doc string) {
	t.Helper()

	res, err := http.Post(adminURL+"/mocks", "application/yaml", strings.NewReader(doc))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestCLI_ServesTheAdminAPI(t *testing.T) {
	// The command serves the admin API on its own address, and prints it, so a user can add mocks while it runs.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	addr := randomAddr(t)
	adminURL, _ := startAdmin(t, "--addr", addr, "--log-level", "disabled")
	require.True(t, strings.HasPrefix(adminURL, "http://127.0.0.1:"), adminURL)

	postMock(t, adminURL, "request: {method: GET, path: /added}\nresponse: {status: 201}\n")

	status, _ := firstAnswer(t, "http://"+addr+"/added")
	require.Equal(t, http.StatusCreated, status)
}

func TestCLI_KeepsTheAdminMocksAcrossRestarts(t *testing.T) {
	// The mocks the admin API adds are kept in testdata/vlatte/_admin, so the next run serves them. --admin-dir moves them,
	// and an empty one keeps them in memory.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	addr := randomAddr(t)
	adminURL, stop := startAdmin(t, "--addr", addr, "--log-level", "disabled")
	postMock(t, adminURL, "id: kept\nrequest: {method: GET, path: /kept}\nresponse: {status: 201}\n")
	require.FileExists(t, filepath.Join("testdata", "vlatte", "_admin", "kept.mock.yaml"))
	require.Zero(t, stop())

	start(t, "--addr", addr, "--log-level", "disabled")

	status, _ := firstAnswer(t, "http://"+addr+"/kept")
	require.Equal(t, http.StatusCreated, status)

	t.Run("in memory", func(t *testing.T) {
		t.Chdir(t.TempDir())

		adminURL, _ := startAdmin(t, "--addr", randomAddr(t), "--admin-dir=", "--log-level", "disabled")
		postMock(t, adminURL, "id: gone\nrequest: {method: GET, path: /gone}\nresponse: {status: 201}\n")

		_, err := os.Stat(filepath.Join("testdata", "vlatte", "_admin"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestCLI_AdminSettings(t *testing.T) {
	// --admin-addr moves the admin API, and --admin=false turns it off: settings apply after the command's default.
	withoutDockerHost(t)

	t.Run("an address", func(t *testing.T) {
		admin := randomAddr(t)
		start(t, "--addr", randomAddr(t), "--admin-addr", admin, "--log-level", "disabled")

		status, body := firstAnswer(t, "http://"+admin+"/health")
		require.Equal(t, http.StatusOK, status)
		require.Contains(t, body, `"kind": "http"`)
	})

	t.Run("off", func(t *testing.T) {
		addr := randomAddr(t)

		var stdout lockedBuffer
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() {
			done <- run(ctx, []string{"--addr", addr, "--admin=false", "--log-level", "disabled"}, &stdout, io.Discard)
		}()
		t.Cleanup(func() { cancel(); <-done })

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Contains(c, stdout.String(), "Listening: ")
		}, 5*time.Second, 10*time.Millisecond)

		require.NotContains(t, stdout.String(), "Admin API")
	})
}

// proxyAnswer sends a GET for target through the forward proxy at addr, once the CLI listens, and returns the answer.
func proxyAnswer(t *testing.T, addr, target string) (int, string) {
	t.Helper()

	proxyURL, err := url.Parse("http://" + addr)
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	client := &http.Client{Transport: transport}

	var res *http.Response

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		r, err := client.Get(target)
		if assert.NoError(c, err) {
			res = r
		}
	}, 10*time.Second, 100*time.Millisecond)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(body)
}

func TestCLI_IsAForwardProxy(t *testing.T) {
	// --proxy makes the CLI a forward proxy: a mock answers the request it matches, whatever its host, and the proxy
	// sends the rest on to their hosts.
	withoutDockerHost(t)

	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "host "+r.URL.Path)
	}))
	t.Cleanup(host.Close)

	mocks := filepath.Join(t.TempDir(), "proxy.mock.yaml")
	require.NoError(t, os.WriteFile(mocks, []byte("request: {method: GET, path: /mocked}\nresponse: {status: 200, body: mocked}\n"), 0o600))

	addr := randomAddr(t)
	start(t, "--addr", addr, "--proxy", "--mock-files", mocks, "--log-level", "disabled")

	status, body := proxyAnswer(t, addr, host.URL+"/mocked")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "mocked", body)

	status, body = proxyAnswer(t, addr, host.URL+"/other")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "host /other", body)
}

func TestCLI_ProxiesWhatNoMockMatches(t *testing.T) {
	// --proxy-all-target sends what no mock matches on to the target, as WireMock's --proxy-all does. With --record,
	// the target's answers are recorded, and a later run serves them without the target.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	mocks := filepath.Join(t.TempDir(), "mocked.mock.yaml")
	require.NoError(t, os.WriteFile(mocks, []byte("request: {method: GET, path: /mocked}\nresponse: {status: 200, body: mocked}\n"), 0o600))

	addr := randomAddr(t)
	stop := start(t, "--addr", addr, "--mock-files", mocks, "--proxy-all-target", target.URL, "--record", "--log-level", "disabled")

	status, body := firstAnswer(t, "http://"+addr+"/mocked")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "mocked", body)

	status, body = firstAnswer(t, "http://"+addr+"/other")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "target /other", body)

	require.Equal(t, 0, stop(), "a stopped server exits cleanly")
	target.Close()

	addr = randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled")

	status, body = firstAnswer(t, "http://"+addr+"/other")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "target /other", body, "the recording of what proxy_all answered")
}

// writeCA writes the certificate and the key of a CA, as PEM files, into dir.
func writeCA(t *testing.T, dir string) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca-key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}), 0o600))

	return certFile, keyFile
}

func TestCLI_InterceptsHTTPS(t *testing.T) {
	// --proxy-intercept, with the CA's files, has the forward proxy serve the TLS of its tunnels: a client that trusts
	// the CA gets the mocks' answers to its HTTPS requests, for any host.
	withoutDockerHost(t)

	dir := t.TempDir()
	certFile, keyFile := writeCA(t, dir)

	mocks := filepath.Join(dir, "https.mock.yaml")
	require.NoError(t, os.WriteFile(mocks, []byte("request: {method: GET, path: /mocked}\nresponse: {status: 200, body: mocked}\n"), 0o600))

	addr := randomAddr(t)
	start(t, "--addr", addr, "--proxy-intercept", "--proxy-ca-cert", certFile, "--proxy-ca-key", keyFile,
		"--mock-files", mocks, "--log-level", "disabled")

	ca, err := os.ReadFile(certFile)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca))

	proxyURL, err := url.Parse("http://" + addr)
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}}
	t.Cleanup(transport.CloseIdleConnections)

	client := &http.Client{Transport: transport}

	var body []byte

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := client.Get("https://api.example.com/mocked")
		if !assert.NoError(c, err) {
			return
		}

		defer res.Body.Close()

		body, err = io.ReadAll(res.Body)
		assert.NoError(c, err)
	}, 10*time.Second, 100*time.Millisecond)

	require.Equal(t, "mocked", string(body))
}

func TestCLI_KeepsAJournalOfRequests(t *testing.T) {
	// The command keeps the requests it serves in testdata/vlatte/_journal, across restarts, which the admin API queries.
	// --journal=false turns it off.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	count := func(adminURL string) (int, map[string]any) {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), "QUERY", adminURL+"/requests", strings.NewReader(`{"path": "/x", "limit": 0}`))
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer res.Body.Close()

		var doc map[string]any
		require.NoError(t, json.UnmarshalRead(res.Body, &doc))

		return res.StatusCode, doc
	}

	addr := randomAddr(t)
	adminURL, stop := startAdmin(t, "--addr", addr, "--log-level", "disabled")
	status, _ := firstAnswer(t, "http://"+addr+"/x")
	require.Equal(t, mockhttp.StatusNoMatch, status)

	code, doc := count(adminURL)
	require.Equal(t, http.StatusOK, code, doc)
	require.Equal(t, float64(1), doc["count"])
	require.Zero(t, stop())
	require.DirExists(t, filepath.Join("testdata", "vlatte", "_journal"))

	adminURL, _ = startAdmin(t, "--addr", addr, "--log-level", "disabled")
	code, doc = count(adminURL)
	require.Equal(t, http.StatusOK, code, doc)
	require.Equal(t, float64(1), doc["count"], "the request of the first run")

	t.Run("off", func(t *testing.T) {
		t.Chdir(t.TempDir())

		adminURL, _ := startAdmin(t, "--addr", randomAddr(t), "--journal=false", "--log-level", "disabled")
		code, _ := count(adminURL)
		require.Equal(t, http.StatusNotImplemented, code)
		require.NoDirExists(t, filepath.Join("testdata", "vlatte", "_journal"))
	})
}

func TestCLI_NamedServersKeepTheirOwnFiles(t *testing.T) {
	// Two servers in one directory each keep their files, the journal's lock included, in a folder of their name:
	// both start. --workdir puts a server's files where it says.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	for _, name := range []string{"a", "b"} {
		addr := randomAddr(t)
		adminURL, _ := startAdmin(t, "--addr", addr, "--name", name, "--log-level", "disabled")
		postMock(t, adminURL, "id: kept\nrequest: {method: GET, path: /kept}\nresponse: {status: 201}\n")

		status, _ := firstAnswer(t, "http://"+addr+"/kept")
		require.Equal(t, http.StatusCreated, status)
	}

	for _, name := range []string{"a", "b"} {
		require.DirExists(t, filepath.Join("testdata", "vlatte", name, "_journal"))
		require.FileExists(t, filepath.Join("testdata", "vlatte", name, "_admin", "kept.mock.yaml"))
	}

	adminURL, _ := startAdmin(t, "--addr", randomAddr(t), "--workdir", "elsewhere", "--log-level", "disabled")
	postMock(t, adminURL, "id: moved\nrequest: {method: GET, path: /moved}\nresponse: {status: 201}\n")
	require.FileExists(t, filepath.Join("elsewhere", "_admin", "moved.mock.yaml"))
	require.DirExists(t, filepath.Join("elsewhere", "_journal"))
}
