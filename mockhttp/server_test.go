package mockhttp

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// echo answers 202 and tells, in headers, the method and the path the mock got.
func echo() *FuncReply {
	return Handler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Method", r.Method)
		w.Header().Set("X-Path", r.URL.EscapedPath())
		w.WriteHeader(http.StatusAccepted)
	})
}

func TestServer_EveryMethodReachesTheMocks(t *testing.T) {
	// A mock can match any method, so the server must not turn one away before the mocks are looked up: extension
	// methods and methods in another case reach them as sent.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(AnyMethod().Reply(echo()))

	methods := []string{"PURGE", "PROPFIND", "REPORT", "LINK", "MKCOL", "get", "Post", "purge"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			res, _ := do(t, method, m.URL("/p"), nil, nil)

			require.Equal(t, http.StatusAccepted, res.StatusCode)
			require.Equal(t, method, res.Header.Get("X-Method"))
		})
	}

	vlatte.Times(t, scope, int64(len(methods)))
}

func TestServer_MethodMatchesIgnoringCase(t *testing.T) {
	// Mock files and Builder.Method match the method ignoring case, so a request in another case gets that mock.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Request().Method("PURGE").URLPathf("/purge").Reply(Accepted()),
		Getf("/get").Reply(Accepted()))

	tests := []struct {
		giveMethod string
		givePath   string
	}{
		{giveMethod: "PURGE", givePath: "/purge"},
		{giveMethod: "purge", givePath: "/purge"},
		{giveMethod: "Purge", givePath: "/purge"},
		{giveMethod: "get", givePath: "/get"},
	}

	for _, tt := range tests {
		t.Run(tt.giveMethod+" "+tt.givePath, func(t *testing.T) {
			res, _ := do(t, tt.giveMethod, m.URL(tt.givePath), nil, nil)

			require.Equal(t, http.StatusAccepted, res.StatusCode)
		})
	}
}

func TestServer_AnUnmatchedMethodGetsTheNoMatchStatus(t *testing.T) {
	// A method no mock matches is a no-match like any other: the server's no-match status and VLatte-Error tell it
	// apart from a 405 a mock replies with.
	tests := []struct {
		name       string
		giveConfig *ConfigBuilder
		wantStatus int
	}{
		{
			name:       "default status",
			giveConfig: Setup().Silent(),
			wantStatus: StatusNoMatch,
		},
		{
			name:       "configured status",
			giveConfig: Setup().Silent().MockNotFoundStatusCode(http.StatusNotFound),
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := MustNew(tt.giveConfig)
			m.MustStart()
			t.Cleanup(m.Close)

			scope := m.MustMock(Getf("/p").Reply(OK()))

			res, _ := do(t, "PURGE", m.URL("/p"), nil, nil)

			require.Equal(t, tt.wantStatus, res.StatusCode)
			require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(HeaderVlatteError))
			vlatte.NotCalled(t, scope)
		})
	}
}

func TestServer_ExtensionMethodsOverHTTP2(t *testing.T) {
	// HTTP/2 carries the method in a pseudo-header: an extension method reaches the mocks over it too.
	m := MustNew(Setup().Silent())
	m.MustStartTLS()
	t.Cleanup(m.Close)

	m.MustMock(AnyMethod().Reply(echo()))

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, Protocols: new(http.Protocols)}
	tr.Protocols.SetHTTP2(true)
	t.Cleanup(tr.CloseIdleConnections)

	req, err := http.NewRequestWithContext(t.Context(), "PURGE", m.URL("/p"), nil)
	require.NoError(t, err)

	res, err := (&http.Client{Transport: tr}).Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, 2, res.ProtoMajor)
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	require.Equal(t, "PURGE", res.Header.Get("X-Method"))
}

func TestServer_PathsReachTheMocksAsSent(t *testing.T) {
	// Mocks match the path the client sent: the server must not clean it, or redirect to a cleaned one.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(AnyMethod().Reply(echo()))

	for _, path := range []string{"/a//b", "/a/./b", "/a/../b", "/a/b/", "/a%2Fb"} {
		t.Run(path, func(t *testing.T) {
			res, _ := do(t, http.MethodGet, m.URL()+path, nil, nil)

			require.Equal(t, http.StatusAccepted, res.StatusCode)
			require.Equal(t, path, res.Header.Get("X-Path"))
		})
	}
}

func TestServer_TheAdminAPIIsApartFromTheMocks(t *testing.T) {
	// The admin API serves on an address of its own: every path of the mock server, /__admin included, is the mocks'.
	m := MustNew(Setup().Silent().Admin(""))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(AnyMethod().Reply(echo()))

	require.NotEmpty(t, m.AdminURL())
	require.NotEqual(t, m.URL(), m.AdminURL())

	for _, path := range []string{"/__admin/mocks", "/__admin/ping", "/mocks", "/health"} {
		t.Run(path, func(t *testing.T) {
			res, _ := do(t, http.MethodGet, m.URL()+path, nil, nil)

			require.Equal(t, http.StatusAccepted, res.StatusCode)
			require.Equal(t, path, res.Header.Get("X-Path"))
		})
	}

	res, body := do(t, http.MethodGet, m.AdminURL()+"/health", nil, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.JSONEq(t, `{"status": "ok", "kind": "http", "url": "`+m.URL()+`"}`, string(body))
}

func TestServer_NoAdminAPIUnlessAsked(t *testing.T) {
	// A server opens one port unless its admin API is asked for: tests start many servers.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	require.Empty(t, m.AdminURL())
}

func TestServer_AdminAPIThatCantListen(t *testing.T) {
	// The admin API failing to listen fails the start, which closes the server, mocks and all.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	m := MustNew(Setup().Silent().Admin(taken.Addr().String()))
	t.Cleanup(m.Close)

	require.ErrorContains(t, m.Start(), "admin: ")
	require.ErrorIs(t, m.Start(), http.ErrServerClosed)
}
