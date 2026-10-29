package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// stubMock is the least a store needs.
type stubMock struct {
	id string
}

func (m *stubMock) ID() string            { return m.id }
func (m *stubMock) Name() string          { return "" }
func (m *stubMock) Priority() int         { return 0 }
func (m *stubMock) Hits() int64           { return 0 }
func (m *stubMock) Called() bool          { return false }
func (m *stubMock) Enabled() bool         { return true }
func (m *stubMock) Enable()               {}
func (m *stubMock) Disable()              {}
func (m *stubMock) Events() vlatte.Events { return vlatte.Events{} }
func (m *stubMock) String() string        { return m.id }

// blockingFiles holds a reload until released.
type blockingFiles struct {
	entered chan struct{}
	release chan struct{}
}

func (f *blockingFiles) Reload() error {
	close(f.entered)
	<-f.release

	return nil
}

func (f *blockingFiles) SaveMocks(context.Context, string, string) ([]string, error) {
	return nil, nil
}

func newServer(t *testing.T, opts Options, caps adminapi.Capabilities) *Server {
	t.Helper()

	caps.Mocks = adminapi.MocksOf[*stubMock](vlatte.NewStore[*stubMock](), nil)

	s, err := New(opts, caps)
	require.NoError(t, err)

	return s
}

func TestServer_RoutesAreTheDocumentedOnes(t *testing.T) {
	// The OpenAPI document is written by hand: every route must be in it, and it must document no other.
	doc, err := config.Parse(handler.OpenAPIDocument, "yaml")
	require.NoError(t, err)

	paths, _ := doc.(map[string]any)["paths"].(map[string]any)

	var documented []string
	for path, item := range paths {
		for method := range item.(map[string]any) {
			if method != "parameters" {
				documented = append(documented, strings.ToUpper(method)+" "+path)
			}
		}
	}

	routes := make([]string, 0, len(_routes))
	for _, route := range _routes {
		// GET /{$} is the root alone, which the document names /.
		routes = append(routes, strings.TrimSuffix(route.pattern, "{$}"))
	}

	require.ElementsMatch(t, routes, documented)
}

func TestServer_ListensOnItsOwnAddress(t *testing.T) {
	// The admin API is a server of its own: it listens where it is told, apart from the mocks it manages.
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := free.Addr().String()
	require.NoError(t, free.Close())

	s := newServer(t, Options{Addr: addr}, adminapi.Capabilities{})
	require.Empty(t, s.URL(), "nothing listens before Start")

	require.NoError(t, s.Start())
	require.Equal(t, "http://"+addr, s.URL())

	res, err := http.Get(s.URL() + "/health")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	require.NoError(t, s.Close())

	_, err = net.DialTimeout("tcp", addr, time.Second)
	require.Error(t, err, "nothing listens once closed")
}

func TestServer_StartsOnce(t *testing.T) {
	// As the mock servers do: a server starts once, a closed one doesn't start again, even one that never started,
	// and a start that fails to listen closes it.
	t.Run("twice", func(t *testing.T) {
		s := newServer(t, Options{}, adminapi.Capabilities{})
		require.NoError(t, s.Start())
		t.Cleanup(func() { _ = s.Close() })

		require.ErrorIs(t, s.Start(), vlatte.ErrStarted)
	})

	t.Run("closed before it started", func(t *testing.T) {
		s := newServer(t, Options{}, adminapi.Capabilities{})
		require.NoError(t, s.Close())

		require.ErrorIs(t, s.Start(), http.ErrServerClosed)
		require.Empty(t, s.URL())
	})

	t.Run("a start that fails", func(t *testing.T) {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = taken.Close() })

		s := newServer(t, Options{Addr: taken.Addr().String()}, adminapi.Capabilities{})

		require.ErrorContains(t, s.Start(), "admin: ")
		require.ErrorIs(t, s.Start(), http.ErrServerClosed)
	})
}

func TestServer_CloseWaitsForTheOperationsItServes(t *testing.T) {
	// The mock server closes its mocks once the admin API closed: an operation still running, such as a reload,
	// would store mocks after that. Close returns once no operation runs.
	files := &blockingFiles{entered: make(chan struct{}), release: make(chan struct{})}
	s := newServer(t, Options{}, adminapi.Capabilities{Files: files})
	require.NoError(t, s.Start())

	reloaded := make(chan int, 1)
	go func() {
		res, err := http.Post(s.URL()+"/mocks/reload", "", nil)
		if err != nil {
			reloaded <- 0
			return
		}

		_ = res.Body.Close()
		reloaded <- res.StatusCode
	}()

	<-files.entered

	closed := make(chan struct{})
	go func() {
		_ = s.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("Close returned while a reload ran")
	case <-time.After(100 * time.Millisecond):
	}

	close(files.release)
	<-closed
	require.Equal(t, http.StatusNoContent, <-reloaded, "the reload ended")
}

func TestNew_NeedsTheMocks(t *testing.T) {
	_, err := New(Options{}, adminapi.Capabilities{})
	require.EqualError(t, err, "admin: no mocks: the capabilities need Mocks")

	require.PanicsWithError(t, "admin: no mocks: the capabilities need Mocks", func() { MustNew(Options{}, adminapi.Capabilities{}) })
}

func TestServer_ListensOnAPort(t *testing.T) {
	// A port alone is an address too, on every interface, as a server in a container needs.
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(free.Addr().String())
	require.NoError(t, err)
	require.NoError(t, free.Close())

	s := newServer(t, Options{Addr: port}, adminapi.Capabilities{})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	res, err := http.Get("http://127.0.0.1:" + port + "/health")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestServer_StartsNothingOnceClosing(t *testing.T) {
	// A request that comes once Close runs, such as on a connection kept alive, starts no operation, so none runs once
	// Close returned.
	s := newServer(t, Options{}, adminapi.Capabilities{})
	require.NoError(t, s.Close())

	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/mocks", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"code": "closing"`)
}

func TestServer_RoutesTheProtocolLacksAnswer501(t *testing.T) {
	// Every route answers, whatever the server's protocol can do: the API is the same for every mock server.
	s := newServer(t, Options{}, adminapi.Capabilities{})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	checked := 0

	for _, route := range _routes {
		if route.lacking == "" {
			continue
		}

		method, path, _ := strings.Cut(route.pattern, " ")
		path = strings.ReplaceAll(path, "{id}", "x")

		r, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusNotImplemented, res.StatusCode, route.pattern)

		checked++
	}

	require.NotZero(t, checked)
}

func TestServer_MiddlewaresWrapEveryRequestInOrder(t *testing.T) {
	// A middleware sees each request before the admin API does, even one the cross-origin check denies, so a check
	// such as authentication leaves no request out. The first one given is the outermost.
	trace := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("X-Trace", name)
				next.ServeHTTP(w, r)
			})
		}
	}

	s := newServer(t, Options{Middlewares: []func(http.Handler) http.Handler{trace("first"), trace("second")}}, adminapi.Capabilities{})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	res, err := http.Get(s.URL() + "/health")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"first", "second"}, res.Header.Values("X-Trace"))

	r, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, s.URL()+"/mocks", nil)
	require.NoError(t, err)
	r.Header.Set("Sec-Fetch-Site", "cross-site")

	res, err = http.DefaultClient.Do(r)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, []string{"first", "second"}, res.Header.Values("X-Trace"))
}

func TestServer_CloseEndsTheStreams(t *testing.T) {
	// A stream never ends on its own: closing the admin API ends it, so nothing of the admin API outlives Close.
	events := adminapi.NewEvents()
	s := newServer(t, Options{}, adminapi.Capabilities{Events: events, Logs: adminapi.NewLogs()})
	require.NoError(t, s.Start())

	for _, path := range []string{"/events", "/logs"} {
		// Not t.Context: it ends before the cleanups that read what is left of the streams.
		r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL()+path, nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode, path)

		ended := make(chan error, 1)
		go func() {
			_, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			ended <- err
		}()

		t.Cleanup(func() { require.NoError(t, <-ended, "the stream of %s ends", path) })
	}

	done := make(chan error, 1)
	go func() { done <- s.Close() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited on the streams")
	}
}

func TestServer_StreamsItLacksAreNotSupported(t *testing.T) {
	// A server that logs through a logger it was given streams no log lines; one without events streams none.
	s := newServer(t, Options{}, adminapi.Capabilities{})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	for path, detail := range map[string]string{
		"/events": "this mock server can't stream its events",
		"/logs":   "this mock server can't stream its logs",
	} {
		res, err := http.Get(s.URL() + path)
		require.NoError(t, err)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		require.Equal(t, http.StatusNotImplemented, res.StatusCode, path)
		require.Contains(t, string(body), `"detail": "`+detail+`"`, path)
	}
}

// connectMCP serves s over stdio and connects a client to it, which the test closes.
func connectMCP(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()

	served := make(chan error, 1)
	go func() {
		served <- s.ServeMCP(context.Background(), serverIn, serverOut)
		_ = serverOut.Close()
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = cs.Close()
		<-served
	})

	return cs
}

func TestServer_CloseWaitsForTheMCPCallsItServes(t *testing.T) {
	// A program that serves MCP over stdio starts no HTTP server, but its calls run operations all the same: Close
	// waits for them, as it does for the requests it serves.
	synctest.Test(t, func(t *testing.T) {
		files := &blockingFiles{entered: make(chan struct{}), release: make(chan struct{})}
		s := newServer(t, Options{MCP: true}, adminapi.Capabilities{Files: files})
		cs := connectMCP(t, s)

		release := sync.OnceFunc(func() { close(files.release) })
		t.Cleanup(release) // a failed test ends the reload, so the client closes

		reloaded := make(chan error, 1)
		go func() {
			_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "reload_mocks"})
			reloaded <- err
		}()

		<-files.entered

		closed := make(chan struct{})
		go func() {
			_ = s.Close()
			close(closed)
		}()

		// Every goroutine of the test is blocked: Close too, as long as the reload runs.
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned while a reload ran")
		default:
		}

		release()
		<-closed
		require.NoError(t, <-reloaded, "the reload ended")
	})
}

func TestServer_ServesMCPOnlyWhenAsked(t *testing.T) {
	// MCP is a door to every operation: a server opens it only when its options say so.
	s := newServer(t, Options{}, adminapi.Capabilities{})

	err := s.ServeMCP(t.Context(), strings.NewReader(""), io.Discard)
	require.EqualError(t, err, "admin: MCP is off: set Options.MCP")
}

func TestServer_StartsNoMCPCallOnceClosing(t *testing.T) {
	// A call that comes once Close runs starts no operation, as a request does not.
	s := newServer(t, Options{MCP: true}, adminapi.Capabilities{})
	cs := connectMCP(t, s)
	require.NoError(t, s.Close())

	_, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "remove_all_mocks"})
	require.ErrorContains(t, err, "admin: the admin API is closing")
}

func TestServer_EveryRouteNeedsCredentials(t *testing.T) {
	// Every route, the pages, the streams and MCP included, needs credentials once they are set, but the probes: a
	// route added later is covered without a test of its own.
	s := newServer(t, Options{MCP: true, Auth: handler.Credentials{Token: "tok"}}, adminapi.Capabilities{
		Events: adminapi.NewEvents(), Logs: adminapi.NewLogs(),
	})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	// A connection each: a 401 can close one, and a dial that loses its race to another sits unused, which Close
	// waits for.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	checked := 0

	for _, route := range _routes {
		if slices.Contains(_probes, route.pattern) {
			continue
		}

		method, path, _ := strings.Cut(strings.TrimSuffix(route.pattern, "{$}"), " ")
		path = strings.NewReplacer("{id}", "x", "{name}", "x").Replace(path)

		r, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, nil)
		require.NoError(t, err)

		res, err := client.Do(r)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, route.pattern)

		checked++
	}

	require.Equal(t, len(_routes)-len(_probes), checked)
}

func TestServer_MiddlewaresRunBeforeTheCredentialsCheck(t *testing.T) {
	// A browser's CORS preflight carries no credentials: a middleware that answers it must see it first.
	preflight := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}

	s := newServer(t, Options{Middlewares: []func(http.Handler) http.Handler{preflight}, Auth: handler.Credentials{Token: "tok"}}, adminapi.Capabilities{})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	r, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, s.URL()+"/mocks", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestServer_ServesHTTPSWithTLS(t *testing.T) {
	// Credentials over plain HTTP are readable on the network: with TLS, the admin API serves HTTPS, and its URL says so.
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)

	s := newServer(t, Options{TLS: &tls.Config{Certificates: cert.TLS.Certificates}}, adminapi.Capabilities{})
	require.NoError(t, s.Start())
	t.Cleanup(func() { _ = s.Close() })

	require.True(t, strings.HasPrefix(s.URL(), "https://"), s.URL())

	// The test server's client trusts its certificate, which the admin API serves.
	res, err := cert.Client().Get(s.URL() + "/health")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestServer_WarnsWhenExposedBeyondTheLocalHost(t *testing.T) {
	// Beyond the local host, anyone on the network reaches the admin API: it warns when nothing keeps them out, or
	// when the credentials that do travel in plain text. On the local host, it says nothing.
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(cert.Close)

	tests := []struct {
		name string
		opts Options
		warn string
	}{
		{name: "no auth", opts: Options{Addr: "0.0.0.0:0"}, warn: "without authentication"},
		{name: "auth over HTTP", opts: Options{Addr: "0.0.0.0:0", Auth: handler.Credentials{Token: "tok"}}, warn: "over plain HTTP"},
		{
			name: "auth over HTTPS",
			opts: Options{Addr: "0.0.0.0:0", Auth: handler.Credentials{Token: "tok"}, TLS: &tls.Config{Certificates: cert.TLS.Certificates}},
		},
		{name: "local host", opts: Options{Addr: "127.0.0.1:0"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := zerolog.New(&logs)

			s := newServer(t, tc.opts, adminapi.Capabilities{Logger: &logger})
			require.NoError(t, s.Start())
			require.NoError(t, s.Close())

			if tc.warn == "" {
				require.Empty(t, logs.String())
				return
			}

			require.Contains(t, logs.String(), `"level":"warn"`)
			require.Contains(t, logs.String(), tc.warn)
		})
	}
}
