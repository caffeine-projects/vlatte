package mockhttp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/match"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}

func TestHTTPMockApp(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			Header("test", match.StrictEqual("hello")).
			Query("filter", match.StrictEqual("all")).
			Reply(Created().
				PlainText("hello world")))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)
	req.Header.Add("test", "hello")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, 201, res.StatusCode)
	require.Equal(t, "hello world", string(body))
}

func TestResponseMapperModifyingResponse(t *testing.T) {
	const k = "key"
	const v = "test-ok"

	params := vlatte.NewMemoryKV()
	err := params.Set(t.Context(), k, []byte(v))
	require.NoError(t, err)

	m := MustNew(Setup().Parameters(params))
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(Get(match.URLPath("/test")).
		Reply(OK()).
		Map(func(rv *RequestValues, r *Response) error {
			val, _ := rv.App.Parameters().Get(t.Context(), k)

			r.Header.Add("x-param-key", string(val))
			r.Header.Add("x-test", rv.RawRequest.Header.Get("x-param"))

			return nil
		}))

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/test", m.URL()), nil)
	req.Header.Add("x-param", "dev")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "dev", res.Header.Get("x-test"))
	require.Equal(t, "test-ok", res.Header.Get("x-param-key"))
	require.True(t, scoped.AssertCalled(t))
}

func TestErrors(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test2")).
			Header("test", match.Function(
				func(_ any) (bool, error) {
					return false, errors.New("failed")
				})).
			Reply(OK()))

	res, err := http.Get(fmt.Sprintf("%s/test2", m.URL()))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.False(t, scoped.Called())
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureMatch, res.Header.Get(HeaderVlatteError), "a failing matcher is not a missing mock")
}

func TestHTTPMockAppAssertions(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	ft := testmock.NewFakeT()

	scoped := m.MustMock(
		Get(match.URLPath("/test-ok")).
			Reply(OK()))

	require.EqualValues(t, 0, scoped.Hits())
	require.False(t, m.AssertCalled(ft))
	require.True(t, m.AssertNotCalled(ft))
	require.True(t, m.AssertNumberOfCalls(ft, 0))
	require.EqualValues(t, 0, m.Hits())

	res, err := http.Get(m.URL() + "/test-ok")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.EqualValues(t, 1, scoped.Hits())
	require.True(t, m.AssertCalled(ft))
	require.False(t, m.AssertNotCalled(ft))
	require.True(t, m.AssertNumberOfCalls(ft, 1))
	require.EqualValues(t, 1, m.Hits())
}

func TestHTTPMockAppEnableDisable(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(
		Get(match.URLPath("/test-1")).
			Reply(OK()),
		Get(match.URLPath("/test-2")).
			Reply(OK()))

	res, err := http.Get(m.URL() + "/test-1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	// disable all mocks
	// should return tea pot for all
	m.Disable()

	res, err = http.Get(m.URL() + "/test-1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	res, err = http.Get(m.URL() + "/test-2")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, StatusNoMatch, res.StatusCode)

	// re-enable mocks again
	m.Enable()

	res, err = http.Get(m.URL() + "/test-1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.Get(m.URL() + "/test-2")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestHTTPMockAppSilently(t *testing.T) {
	m := MustNew(Setup().LogVerbosity(vlatte.LogBasic).LogLevel(vlatte.LogLevelNone))
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			Reply(Created().
				PlainText("hello world")))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, 201, res.StatusCode)
	require.Equal(t, string(body), "hello world")
}

func TestSchemeMatching(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			Scheme("http").
			Reply(OK()))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test?filter=all", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestHTTPMockAppNoReply(t *testing.T) {
	m := MustNew()

	scoped, err := m.Mock(Get(match.URLPath("/test")))
	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestHTTPMockAppNoMatchers(t *testing.T) {
	m := MustNew()

	scoped, err := m.Mock(Request())
	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestHTTPMockApp_RequestMatches(t *testing.T) {
	m := MustNew()
	m.MustStart()

	defer m.Close()

	scoped := m.MustMock(
		Get(match.URLPath("/test")).
			RequestMatches(func(r *http.Request) (bool, error) {
				if r.Method == http.MethodGet {
					return true, nil
				}

				return false, nil
			}).
			Reply(OK()))

	httpClient := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)

	res, err := httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
	require.Equal(t, http.StatusOK, res.StatusCode)

	req, _ = http.NewRequest(http.MethodPost, m.URL()+"/test", nil)
	res, err = httpClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	vlatte.Times(t, scoped, 1)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestHTTPMockAppConcurrency(t *testing.T) {
	m := MustNew()
	jobs := 10
	wg := sync.WaitGroup{}

	for i := range jobs {
		wg.Add(1)
		go func(index int) {
			m.MustMock(Getf("%s", "/test--"+strconv.FormatInt(int64(index), 10)).Reply(OK()))
			m.MustMock(Getf("%s", "/concurrency--"+strconv.FormatInt(int64(index), 10)).Reply(Accepted()))

			m.Hits()
			m.Disable()
			m.Enable()

			m.Clean(t.Context())

			m.MustMock(Getf("%s", "/test--"+strconv.FormatInt(int64(index), 10)).Reply(OK()))
			m.MustMock(Getf("%s", "/concurrency--"+strconv.FormatInt(int64(index), 10)).Reply(Accepted()))

			wg.Done()
		}(i)

		m.MustMock(Getf("%s", "/test-outside--"+strconv.FormatInt(int64(i), 10)).Reply(Created()))
		m.Clean(t.Context())

		m.Hits()
		m.Disable()
		m.Enable()
	}

	m.Hits()
	m.MustMock(Getf("/test-final").Reply(BadRequest()))
	m.MustStart()
	m.Close()

	wg.Wait()

	t.Log("done")
}

func TestHTTPMockAppConcurrentRequests(t *testing.T) {
	jobs := 20
	wg := sync.WaitGroup{}
	httpClient := &http.Client{}

	m := MustNew()
	m.MustStart()

	defer m.Close()

	// get must not stop the goroutine with require: FailNow is only valid on the test goroutine.
	get := func(path string) int {
		res, err := httpClient.Get(m.URL() + path)
		if !assert.NoError(t, err) {
			return 0
		}

		assert.NoError(t, res.Body.Close())

		return res.StatusCode
	}

	for i := range jobs {
		wg.Go(func() {
			num := strconv.FormatInt(int64(i), 10)

			scope1 := m.MustMock(Getf("%s", "/test--"+num).Reply(OK()))
			scope2 := m.MustMock(Getf("%s", "/concurrency--"+num).Reply(Accepted()))
			scope3 := m.MustMock(Getf("%s", "/seq--"+num).Reply(Seq(OK(), BadRequest())))
			scope4 := m.MustMock(Getf("%s", "/rand--"+num).Reply(Rand(Accepted(), InternalServerError())))

			assert.Equal(t, http.StatusOK, get("/test--"+num))

			m.MustMock(Getf("%s", "/test-after--"+num).Reply(BadRequest()))

			assert.Equal(t, http.StatusAccepted, get("/concurrency--"+num))

			m.MustMock(Getf("%s", "/concurrency-after--"+num).Reply(InternalServerError()))

			assert.Equal(t, http.StatusOK, get("/seq--"+num))
			assert.Equal(t, http.StatusBadRequest, get("/seq--"+num))

			for range 50 {
				code := get("/rand--" + num)
				assert.True(t, code == http.StatusAccepted || code == http.StatusInternalServerError)
			}

			scope1.AssertCalled(t)
			vlatte.Times(t, scope1, 1)
			scope2.AssertCalled(t)
			vlatte.Times(t, scope2, 1)
			scope3.AssertCalled(t)
			vlatte.Times(t, scope3, 2)
			scope4.AssertCalled(t)
			vlatte.Times(t, scope4, 50)

			m.MustMock(Getf("%s", "/test-final--"+num).Reply(PartialContent()))
		})
	}

	wg.Wait()

	require.EqualValues(t, 54*jobs, m.Hits())
}

func TestDefaultLoggerIncludesServerName(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)

	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = stdout })

	log := (&Server{}).getLog(&Config{Name: "srv"}, nil)
	log.Info().Msg("hello")
	require.NoError(t, w.Close())

	b, err := io.ReadAll(r)
	require.NoError(t, err)
	// Log lines must name the server, so the logs of several mock servers can be told apart.
	require.Contains(t, string(b), `"server":"srv"`)
}

func TestURL_InvalidPathPanicsInsteadOfExiting(t *testing.T) {
	m := MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	// Path elements must already be escaped, so "50%" is invalid. A library must not exit the process.
	require.Panics(t, func() { m.URL("50%") })
}

func TestURL_TheLastElementKeepsItsQueryAndFragment(t *testing.T) {
	// In a URL, a "?" starts the query and a "#" the fragment. Escaped into the path, as url.JoinPath does, they make
	// a request for another path, with no query.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		give []string
		want string
	}{
		{give: []string{"/test"}, want: "/test"},
		{give: []string{"/test?q=none"}, want: "/test?q=none"},
		{give: []string{"/test#contacts"}, want: "/test#contacts"},
		{give: []string{"/users", "1?active=true#top"}, want: "/users/1?active=true#top"},
		{give: []string{"/users", "?active=true"}, want: "/users?active=true"},
		{give: []string{"/users/?active=true"}, want: "/users/?active=true"},
		// Escaped, a "?" is part of the path.
		{give: []string{"/test%3Fq=none"}, want: "/test%3Fq=none"},
		// Only the last element has a query: before it, a "?" is part of the path.
		{give: []string{"/a?b", "c"}, want: "/a%3Fb/c"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.give), func(t *testing.T) {
			require.Equal(t, m.URL()+tt.want, m.URL(tt.give...))
		})
	}
}

func TestURL_LeavesThePathsItIsGiven(t *testing.T) {
	// URL(paths...) shares the caller's slice: cutting the query off the last element must not change it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	paths := []string{"/users", "1?active=true"}
	m.URL(paths...)

	require.Equal(t, []string{"/users", "1?active=true"}, paths)
}

func TestSettingOnlyPort(t *testing.T) {
	// Binding a custom address is covered by TestConfig; Port must produce a port-only address.
	m := MustNew(Setup().Port(12345))

	require.Equal(t, ":12345", m.Config().Addr)
}

func TestHTTPMockApp_Server(t *testing.T) {
	m := MustNew()
	srv := m.Backend()

	require.NotNil(t, srv)
	require.IsType(t, &httptestBackend{}, srv)
}

type failingConfigurer struct{}

func (failingConfigurer) Apply(*Config) error { return errors.New("bad config") }

type failingBackendFactory struct{}

func (failingBackendFactory) NewBackend(BackendConfig) (Backend, error) {
	return nil, errors.New("no server")
}

func TestNew_ReturnsErrors(t *testing.T) {
	_, err := New(failingConfigurer{})
	require.ErrorContains(t, err, "bad config")

	_, err = New(Setup().BackendFactory(failingBackendFactory{}))
	require.ErrorContains(t, err, "no server")

	require.Panics(t, func() { MustNew(failingConfigurer{}) })
}

// loaderFunc is a Loader that runs a function, such as one that is slow, fails, or closes the server.
type loaderFunc func(app *Server) error

func (f loaderFunc) Load(app *Server) error { return f(app) }

var errLoad = errors.New("a mock file that doesn't load")

// startWhileRequested starts a server built with setup while a client sends it a GET of path: the client connects
// once the server listens, before the mocks load, and the mocks of builders load after it sent the request. It returns
// the answer the client got.
func startWhileRequested(t *testing.T, setup *ConfigBuilder, path string, builders ...vlatte.Builder[*Mock, *Server]) (*Server, *http.Response, string) {
	t.Helper()

	handled := make(chan struct{}, 1)

	var conn net.Conn

	m := MustNew(setup.
		Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case handled <- struct{}{}:
				default:
				}

				next.ServeHTTP(w, r)
			})
		}).
		Loader(loaderFunc(func(app *Server) error {
			// Loaders run on the goroutine that called Start: here, the test's.
			u, err := url.Parse(app.URL())
			require.NoError(t, err, "the server listens before its loaders run, so they can read URL")

			conn, err = net.Dial("tcp", u.Host)
			require.NoError(t, err)
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

			_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", path, u.Host)
			require.NoError(t, err)

			// Showing the server doesn't serve takes a wait: a server that serves takes the request well within it.
			select {
			case <-handled:
				t.Error("the server served a request before it loaded its mocks")
			case <-time.After(100 * time.Millisecond):
			}

			_, err = app.Mock(builders...)

			return err
		})))
	t.Cleanup(m.Close)

	require.NoError(t, m.Start())
	t.Cleanup(func() { _ = conn.Close() })

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return m, res, string(body)
}

// requireRefused fails the test when addr accepts a connection.
func requireRefused(t *testing.T, addr, msg string) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
	}

	require.Error(t, err, msg)
}

func TestServer_ARequestThatComesWhileTheServerStartsGetsTheMockFiles(t *testing.T) {
	// #54: the server served before its loaders ran, so a client that connected as soon as the port opened, such as a
	// health check, got the no-match answer though a mock file matched its request.
	_, res, _ := startWhileRequested(t, Setup().Silent(), "/ping", Getf("/ping").Reply(OK()))

	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestServer_ARequestThatComesWhileTheServerStartsIsRecorded(t *testing.T) {
	// #54: the recording the config sets started after the server served, so with --record a request that came first
	// got the no-match answer instead of the target's, and wasn't recorded.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "target") })
	dir := t.TempDir()

	m, res, body := startWhileRequested(t, Setup().Silent().Record(RecordDir(dir), RecordTarget(target.URL)), "/a")

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "target", body)

	m.Close()
	require.Len(t, recordedMocks(t, dir), 1, "Close writes what the recording took")
}

func TestServer_AStartThatFailsClosesTheServer(t *testing.T) {
	// #54: a start whose mocks failed to load returned the error with the server still serving, without mocks, and
	// the CLI exited without closing it. A start that fails leaves nothing listening.
	for name, start := range map[string]func(*Server) error{
		"Start":    (*Server).Start,
		"StartTLS": (*Server).StartTLS,
	} {
		t.Run(name, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)

			m := MustNew(Setup().Silent().Listener(l).Loader(loaderFunc(func(*Server) error { return errLoad })))
			t.Cleanup(m.Close)

			require.ErrorIs(t, start(m), errLoad)
			require.Empty(t, m.URL())
			requireRefused(t, l.Addr().String(), "a start that fails leaves nothing listening")

			require.ErrorIs(t, start(m), http.ErrServerClosed, "a start that fails closes the server")
		})
	}
}

func TestServer_CloseLogsAJournalThatFailsToClose(t *testing.T) {
	// Close returns nothing: a journal that can't write the index of its last segment, which the next server of its
	// directory opens fast from, is logged, not lost.
	logs := &bytes.Buffer{}
	logger := zerolog.New(logs)

	dir := t.TempDir()
	m := MustNew(Setup().Logger(&logger).Journal(dir))
	m.MustStart()
	t.Cleanup(m.Close)

	res, _ := do(t, http.MethodGet, m.URL("/nope"), nil, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)

	// A directory where the index of the segment goes: no file can take its place.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "journal-000001.idx"), 0o700))

	m.Close()
	require.Contains(t, logs.String(), "server: Close: closing the journal")
}

func TestServer_ACloseDuringTheStartStopsIt(t *testing.T) {
	// A Close that comes while the server loads its mocks wins: the start fails, and nothing serves.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Silent().Listener(l).Loader(loaderFunc(func(app *Server) error {
		app.Close()
		return nil
	})))
	t.Cleanup(m.Close)

	require.ErrorIs(t, m.Start(), http.ErrServerClosed)
	require.Empty(t, m.URL())
	requireRefused(t, l.Addr().String(), "a closed server serves nothing")
}

func TestServer_StartsOnce(t *testing.T) {
	// #54: a second Start built a second backend on a new address, and Close closed only that one: the first kept
	// serving. A server starts once.
	for name, start := range map[string]func(*Server) error{
		"Start":    (*Server).Start,
		"StartTLS": (*Server).StartTLS,
	} {
		t.Run(name, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			t.Cleanup(m.Close)

			require.NoError(t, start(m))
			first := m.URL()

			require.ErrorIs(t, start(m), vlatte.ErrStarted)
			require.Equal(t, first, m.URL(), "the server serves where it started")

			u, err := url.Parse(first)
			require.NoError(t, err)

			m.Close()
			requireRefused(t, u.Host, "Close leaves nothing serving")
		})
	}
}

func TestServer_WebhookTransportShakesHandsWithinItsLimit(t *testing.T) {
	// The webhook transport the server makes had no TLS handshake timeout, so a webhook whose timeout is above
	// http.DefaultTransport's 10s waited for the whole of it on a target that never shakes hands.
	synctest.Test(t, func(t *testing.T) {
		m := MustNew(Setup().Silent())
		defer m.Close()

		var targets []net.Conn
		defer func() {
			for _, c := range targets {
				_ = c.Close()
			}
		}()

		// The target's end of each connection is never read: the client's hello never gets an answer. A proxy of the
		// environment would be sent a CONNECT first, which never gets one either.
		m.webhooks.Proxy = nil
		m.webhooks.DialContext = func(context.Context, string, string) (net.Conn, error) {
			target, client := net.Pipe()
			targets = append(targets, target)

			return client, nil
		}

		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://hooks.test/", nil)
		require.NoError(t, err)

		start := time.Now()
		_, err = m.webhookTransport().RoundTrip(req)

		require.ErrorContains(t, err, "TLS handshake timeout")
		require.Equal(t, 10*time.Second, time.Since(start))
	})
}

func TestServer_WebhookTransportHasTheDefaultLimits(t *testing.T) {
	// The webhook transport is the server's, so Close closes its idle connections, and it has the limits webhooks had
	// on http.DefaultTransport: it kept idle connections 15s, and had no dial timeout. The dialer's own timeout can't
	// be seen from here: a dialer must be set.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	want := http.DefaultTransport.(*http.Transport)
	got := m.webhooks

	require.Equal(t, want.IdleConnTimeout, got.IdleConnTimeout)
	require.Equal(t, want.MaxIdleConns, got.MaxIdleConns)
	require.Equal(t, want.TLSHandshakeTimeout, got.TLSHandshakeTimeout)
	require.Equal(t, want.ExpectContinueTimeout, got.ExpectContinueTimeout)
	require.Equal(t, want.ForceAttemptHTTP2, got.ForceAttemptHTTP2)
	require.NotNil(t, got.Proxy)
	require.NotNil(t, got.DialContext)
}
