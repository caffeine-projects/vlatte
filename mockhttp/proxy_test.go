package mockhttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
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

// proxyClient returns a client that sends its requests through m, as its HTTP proxy.
func proxyClient(t *testing.T, m *Server) *http.Client {
	t.Helper()

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	return &http.Client{Transport: transport}
}

func TestForwardProxy_PassesTheHostAnswerOnAsAProxyReplyDoes(t *testing.T) {
	// #40: the forward proxy had code of its own, which lost the host's trailers, as it set them without announcing
	// them, and passed on the hop-by-hop headers of the host's connection, such as Keep-Alive. It answers as proxy
	// replies do, through the same code.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderTrailer, "X-Checksum")
		w.Header().Set("Keep-Alive", "timeout=5")
		_, _ = io.WriteString(w, "body")
		w.Header().Set("X-Checksum", "abc")
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := proxyClient(t, m).Get(target.URL + "/x")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, "body", string(body))
	require.Equal(t, "abc", res.Trailer.Get("X-Checksum"))
	require.Empty(t, res.Header.Values("Keep-Alive"))
	require.Equal(t, "true", res.Header.Get(vlatte.ProxiedHeader))
}

func TestForwardProxy_StreamsTheHostBodyAsItComes(t *testing.T) {
	// #40: the forward proxy copied the host's body without flushing, so the client of a stream, such as server-sent
	// events, got nothing until the stream ended.
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		http.NewResponseController(w).Flush()
		<-release
		_, _ = io.WriteString(w, "data: last\n\n")
	}))
	t.Cleanup(target.Close)
	t.Cleanup(func() { close(release) })

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	client := proxyClient(t, m)
	client.Timeout = 2 * time.Second

	res, err := client.Get(target.URL + "/events")
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	line, err := bufio.NewReader(res.Body).ReadString('\n')
	require.NoError(t, err, "the first event comes while the host still streams")
	require.Equal(t, "data: first\n", line)
}

func TestForwardProxy_ARequestInOriginFormGetsTheNoMatchAnswer(t *testing.T) {
	// #40: a request in origin form names no host, so the proxy can't send it on. It went to the host "" all the same,
	// and got a 503 for its "unsupported protocol scheme": it gets the answer of a request no mock matches, as without
	// the proxy.
	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := http.Get(m.URL("/nothing"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, m.Config().RequestWasNotMatchedStatusCode, res.StatusCode)
	require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(HeaderVlatteError))
	require.Empty(t, res.Header.Get(vlatte.ProxiedHeader))
}

func TestForwardProxy_LogsWhatTheHostAnswered(t *testing.T) {
	// #40: a request the forward proxy sent on was logged when it came, and never again: the log didn't show how it
	// ended. DescribeConfig named the proxy a reverse proxy.
	target := okTarget(t)

	var logs bytes.Buffer
	logger := zerolog.New(&logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false).Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := proxyClient(t, m).Get(target.URL + "/proxied")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Contains(t, logs.String(), "<--- REQUEST PROXIED GET /proxied")
	require.Contains(t, logs.String(), `"status":200`)
	require.Contains(t, m.DescribeConfig(), "Forward Proxy: enabled")
}

func TestForwardProxy_AFailedCallIsVlattesAnswer(t *testing.T) {
	// A host the proxy can't reach gets vlatte's answer to a failure, as a proxy reply does, with the error. It isn't
	// marked as proxied: the host didn't answer.
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := proxyClient(t, m).Get(closed.URL + "/gone")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
	require.Contains(t, string(body), "proxy: calling the target "+closed.URL+"/gone")
	require.Empty(t, res.Header.Get(vlatte.ProxiedHeader))
}

func TestForwardProxy_TimesOutWithTheCallersTransport(t *testing.T) {
	// #67: the forward proxy's Timeout bounded only the transport it made: with the server's, it bounded nothing, and the
	// proxy waited for the host as long as that transport did.
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(target.Close)
	t.Cleanup(func() { close(release) })

	m := MustNew(Setup().Silent().Transport(newCountingTransport(t)).Proxy(&ProxyConfig{Timeout: 50 * time.Millisecond}))
	m.MustStart()
	t.Cleanup(m.Close)

	client := proxyClient(t, m)
	client.Timeout = 5 * time.Second

	res, err := client.Get(target.URL + "/slow")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Contains(t, string(body), "no response headers within 50ms")
}
