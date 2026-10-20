package admin

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(
		m,
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
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

func newServer(t *testing.T, options ...Option) *Server {
	t.Helper()

	s, err := New(append([]Option{Mocks[*stubMock](vlatte.NewStore[*stubMock](), nil)}, options...)...)
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
		routes = append(routes, route.pattern)
	}

	require.ElementsMatch(t, routes, documented)
}

func TestServer_ListensOnItsOwnAddress(t *testing.T) {
	// The admin API is a server of its own: it listens where it is told, apart from the mocks it manages.
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := free.Addr().String()
	require.NoError(t, free.Close())

	s := newServer(t, Addr(addr))
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
		s := newServer(t)
		require.NoError(t, s.Start())
		t.Cleanup(func() { _ = s.Close() })

		require.ErrorIs(t, s.Start(), vlatte.ErrStarted)
	})

	t.Run("closed before it started", func(t *testing.T) {
		s := newServer(t)
		require.NoError(t, s.Close())

		require.ErrorIs(t, s.Start(), http.ErrServerClosed)
		require.Empty(t, s.URL())
	})

	t.Run("a start that fails", func(t *testing.T) {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = taken.Close() })

		s := newServer(t, Addr(taken.Addr().String()))

		require.ErrorContains(t, s.Start(), "admin: ")
		require.ErrorIs(t, s.Start(), http.ErrServerClosed)
	})
}

func TestServer_CloseWaitsForTheOperationsItServes(t *testing.T) {
	// The mock server closes its mocks once the admin API closed: an operation still running, such as a reload,
	// would store mocks after that. Close returns once no operation runs.
	files := &blockingFiles{entered: make(chan struct{}), release: make(chan struct{})}
	s := newServer(t, Files(files))
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
	_, err := New()
	require.EqualError(t, err, "admin: no mock store: pass the Mocks option")
}

func TestServer_RoutesTheProtocolLacksAnswer501(t *testing.T) {
	// Every route answers, whatever the server's protocol can do: the API is the same for every mock server.
	s := newServer(t)
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
