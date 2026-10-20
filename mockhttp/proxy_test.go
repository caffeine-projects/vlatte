package mockhttp

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForwardProxy_AConnectItCantDialGets503(t *testing.T) {
	// A tunnel to a host the proxy can't reach isn't opened: the client gets the proxy's answer instead.
	closed := httptest.NewServer(http.NotFoundHandler())
	addr := closed.Listener.Addr().String()
	closed.Close()

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	server, err := url.Parse(m.URL())
	require.NoError(t, err)

	conn, err := net.Dial("tcp", server.Host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
}

func TestForwardProxy_AMockOfTheRootLeavesTunnelsAlone(t *testing.T) {
	// A CONNECT names a host, not a path: a mock file mock for url: / and any method doesn't answer it, and the tunnel
	// opens.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through the tunnel")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(FromBytes([]byte("request:\n  url: /\nresponse:\n  status: 200\n  body: the mock\n"), ".yaml"))

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := target.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)

	client := &http.Client{Transport: transport}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "through the tunnel", string(body))

	_, body = do(t, http.MethodGet, m.URL("/"), nil, nil)
	require.Equal(t, "the mock", string(body), "the mock answers the root")
}

func TestForwardProxy_LeavesHopByHopHeadersOut(t *testing.T) {
	// As with proxy replies, hop-by-hop headers are about one connection, the client's to the proxy or the host's to
	// the proxy, so none goes on: Proxy-Authorization holds the client's credentials for the proxy, not for the host,
	// and the client's "Connection: close" closed the proxy's connection to the host. A request's "Te: trailers" goes
	// on. The client's request stays as it came, for the middlewares that read it once the proxy answered.
	type forwarded struct {
		header http.Header
		close  bool
	}

	got := make(chan forwarded, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- forwarded{header: r.Header.Clone(), close: r.Close}

		w.Header().Set(HeaderConnection, "X-Target-Hop")
		w.Header().Set("X-Target-Hop", "1")
		w.Header().Set("Te", "trailers")
		w.Header().Set("X-Target-End", "kept")
	}))
	t.Cleanup(target.Close)

	after := make(chan http.Header, 1)
	m := MustNew(Setup().Silent().Proxy().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			after <- r.Header.Clone()
		})
	}))
	m.MustStart()
	t.Cleanup(m.Close)

	// Go's client sends the user info of its proxy's URL as Proxy-Authorization.
	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)
	proxyURL.User = url.UserPassword("proxy-user", "proxy-pass")

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	req, err := http.NewRequest(http.MethodGet, target.URL+"/x", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderConnection, "close, X-Client-Hop")
	req.Header.Set("X-Client-Hop", "1")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Proxy-Connection", "keep-alive")
	req.Header.Set(HeaderUpgrade, "websocket")
	req.Header.Set("Te", "trailers")
	req.Header.Set("X-Client-End", "kept")

	res, err := (&http.Client{Transport: transport}).Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	f := <-got
	require.False(t, f.close, "the client's Connection: close is for its own connection")

	for _, name := range []string{HeaderConnection, "X-Client-Hop", "Keep-Alive", "Proxy-Authorization", "Proxy-Connection", HeaderUpgrade} {
		require.Empty(t, f.header.Values(name), "the host got %s", name)
	}

	require.Equal(t, "trailers", f.header.Get("Te"))
	require.Equal(t, "kept", f.header.Get("X-Client-End"))

	require.Empty(t, res.Header.Values("X-Target-Hop"), "the client got a header the host's Connection named")
	require.Empty(t, res.Header.Values("Te"), "the client got the host's Te")
	require.Equal(t, "kept", res.Header.Get("X-Target-End"))

	require.Equal(t, "close, X-Client-Hop", (<-after).Get(HeaderConnection), "the client's request stays as it came")
}

func TestForwardProxy_SendsTheClientTrailers(t *testing.T) {
	// A client's trailers come after its body, and the server sets them on the request once it reads the body to its
	// end: the copy the host gets, made before, has them all the same, for a body the server read only part of too.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		got <- r.Trailer.Get("X-Sum")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().MaxBodyParsingLimit(4).Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	// A reader of unknown length goes chunked, with the trailers after it.
	req, err := http.NewRequest(http.MethodPost, target.URL+"/x", io.MultiReader(strings.NewReader("0123456789")))
	require.NoError(t, err)
	req.Trailer = http.Header{"X-Sum": {"abc"}}

	res, err := (&http.Client{Transport: transport}).Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "abc", <-got)
}
