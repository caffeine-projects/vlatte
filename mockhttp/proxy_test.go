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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestForwardProxy_AConnectItCantDialGetsTheFailureAnswer(t *testing.T) {
	// A tunnel to a host the proxy can't reach isn't opened: the client gets the server's answer to a failure instead,
	// as for a request the proxy can't send on.
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
	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
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

// tcpHost starts a TCP server that serves each connection with serve, until the test ends.
func tcpHost(t *testing.T, serve func(net.Conn)) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var wg sync.WaitGroup

	t.Cleanup(func() {
		_ = l.Close()
		wg.Wait()
	})

	wg.Go(func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			wg.Go(func() {
				defer conn.Close()
				serve(conn)
			})
		}
	})

	return l.Addr().String()
}

// echoConn writes back what it reads, until the connection ends.
func echoConn(conn net.Conn) {
	_, _ = io.Copy(conn, conn)
}

// openTunnel sends a CONNECT to host through the proxy at proxyAddr, with ahead right after it, and returns the
// connection and the reader of what comes back, past the proxy's answer, which must be 200.
func openTunnel(t *testing.T, proxyAddr, host, ahead string) (net.Conn, *bufio.Reader) {
	t.Helper()

	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n%s", host, host, ahead)
	require.NoError(t, err)

	br := bufio.NewReader(conn)

	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)

	return conn, br
}

// hostOf returns the host:port of m.
func hostOf(t *testing.T, m *Server) string {
	t.Helper()

	u, err := url.Parse(m.URL())
	require.NoError(t, err)

	return u.Host
}

func TestForwardProxy_ATunnelGetsWhatTheClientSentAhead(t *testing.T) {
	// A client may send its first bytes right behind its CONNECT, before the answer. The server read them with the
	// request, and the tunnel dropped them with the reader it got from Hijack: the host never got them.
	host := tcpHost(t, echoConn)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	_, br := openTunnel(t, hostOf(t, m), host, "sent ahead\n")

	line, err := br.ReadString('\n')
	require.NoError(t, err, "the host didn't get the bytes the client sent ahead")
	require.Equal(t, "sent ahead\n", line)
}

func TestForwardProxy_ATunnelLetsEachSideFinish(t *testing.T) {
	// A tunnel closed both connections once one side ended what it sent: a host that answers after the client's end,
	// as a request-and-reply protocol over a half-closed connection does, had its answer cut. Each side ends its own
	// direction, and the tunnel closes once both did.
	host := tcpHost(t, func(conn net.Conn) {
		got, _ := io.ReadAll(conn)
		_, _ = fmt.Fprintf(conn, "got %q", got)
	})

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	_, err := io.WriteString(conn, "question")
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	answer, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Equal(t, `got "question"`, string(answer))
}

func TestForwardProxy_CloseEndsTheOpenTunnels(t *testing.T) {
	// The server doesn't track a connection it hands over to a tunnel: tunnels went on after Close, and so did their
	// goroutines.
	host := tcpHost(t, echoConn)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	_, err := io.WriteString(conn, "ping\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ping\n", line)

	m.Close()

	_, err = br.ReadString('\n')
	require.ErrorIs(t, err, io.EOF, "the tunnel is still open")
}

func TestForwardProxy_ATunnelGoesThroughVia(t *testing.T) {
	// A CONNECT dialed its host, past Via: on a network that reaches hosts through a proxy only, tunnels failed. It
	// goes through Via, with Via's credentials, as the requests the proxy sends on do.
	host := tcpHost(t, echoConn)

	connects := make(chan *http.Request, 1)
	via := tcpHost(t, func(conn net.Conn) {
		br := bufio.NewReader(conn)

		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		connects <- req

		upstream, err := net.Dial("tcp", req.Host)
		if err != nil {
			return
		}

		defer upstream.Close()

		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")

		go func() {
			_, _ = io.Copy(upstream, br)
			_ = upstream.(*net.TCPConn).CloseWrite()
		}()

		_, _ = io.Copy(conn, upstream)
	})

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: "http://user:pass@" + via}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	_, err := io.WriteString(conn, "through via\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "through via\n", line)

	var req *http.Request
	select {
	case req = <-connects:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel didn't go through Via")
	}

	require.Equal(t, http.MethodConnect, req.Method)
	require.Equal(t, host, req.Host)
	require.Equal(t, "Basic dXNlcjpwYXNz", req.Header.Get("Proxy-Authorization"))
}

func TestForwardProxy_ATunnelThroughItselfFails(t *testing.T) {
	// A Via that is the server itself sent each CONNECT back to the server, which sent it on again, without end. The
	// CONNECT that comes back fails as a loop, and so does the one the client sent.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Silent().Listener(l).Proxy(&ProxyConfig{Via: "http://" + l.Addr().String()}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "the CONNECT went round without end")

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Contains(t, string(body), "the proxy answered the CONNECT with 418")
}

// unwrappingWriter is the writer of a middleware, which unwraps to the writer it wraps.
type unwrappingWriter struct {
	http.ResponseWriter
}

func (w unwrappingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// opaqueWriter is the writer of a middleware, which doesn't unwrap.
type opaqueWriter struct {
	http.ResponseWriter
}

func TestForwardProxy_ATunnelOpensBehindAWriterThatUnwraps(t *testing.T) {
	// #40: a CONNECT took its connection over with w.(http.Hijacker), so behind a middleware that wraps the writer,
	// every tunnel failed with 500 "hijacking is not supported". It opens through a writer that unwraps to one that
	// can, as http.ResponseController does. Behind one that can't, the client gets an answer that says why.
	host := tcpHost(t, echoConn)

	wrapped := func(wrap func(http.ResponseWriter) http.ResponseWriter) *Server {
		m := MustNew(Setup().Silent().Proxy().Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(wrap(w), r) })
		}))
		m.MustStart()
		t.Cleanup(m.Close)

		return m
	}

	t.Run("a writer that unwraps", func(t *testing.T) {
		m := wrapped(func(w http.ResponseWriter) http.ResponseWriter { return unwrappingWriter{w} })

		conn, br := openTunnel(t, hostOf(t, m), host, "")

		_, err := io.WriteString(conn, "wrapped\n")
		require.NoError(t, err)

		line, err := br.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "wrapped\n", line)
	})

	t.Run("a writer that doesn't", func(t *testing.T) {
		m := wrapped(func(w http.ResponseWriter) http.ResponseWriter { return opaqueWriter{w} })

		conn, err := net.Dial("tcp", hostOf(t, m))
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
		require.NoError(t, err)

		res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
		require.NoError(t, err)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
		require.Contains(t, string(body), "a tunnel needs an HTTP/1.1 connection, and a response writer that can hijack it")
	})
}

func TestForwardProxy_LogsTheTunnelsItOpens(t *testing.T) {
	// #40: a CONNECT was logged when it came, and never again.
	host := tcpHost(t, echoConn)

	var logs bytes.Buffer
	logger := zerolog.New(&logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false).Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	_, err := io.WriteString(conn, "logged\n")
	require.NoError(t, err)

	_, err = br.ReadString('\n')
	require.NoError(t, err)

	require.Contains(t, logs.String(), "<--- REQUEST PROXIED CONNECT")
	require.Contains(t, logs.String(), `"status":200`)
}

// socks5Host starts a SOCKS5 proxy without authentication, which sends the address of each connection it opens to
// dialed.
func socks5Host(t *testing.T, dialed chan<- string) string {
	t.Helper()

	return tcpHost(t, func(conn net.Conn) {
		br := bufio.NewReader(conn)

		// The greeting: the version, then the methods the client offers. No authentication is method 0.
		greeting := make([]byte, 2)
		if _, err := io.ReadFull(br, greeting); err != nil {
			return
		}

		if _, err := io.ReadFull(br, make([]byte, greeting[1])); err != nil {
			return
		}

		_, _ = conn.Write([]byte{5, 0})

		// The request: the version, the command, a reserved byte, then the address and the port.
		head := make([]byte, 4)
		if _, err := io.ReadFull(br, head); err != nil {
			return
		}

		var host string

		switch head[3] {
		case 1:
			ip := make([]byte, net.IPv4len)
			_, _ = io.ReadFull(br, ip)
			host = net.IP(ip).String()
		case 3:
			n, _ := br.ReadByte()
			name := make([]byte, n)
			_, _ = io.ReadFull(br, name)
			host = string(name)
		default:
			return
		}

		port := make([]byte, 2)
		if _, err := io.ReadFull(br, port); err != nil {
			return
		}

		addr := net.JoinHostPort(host, strconv.Itoa(int(port[0])<<8|int(port[1])))
		dialed <- addr

		upstream, err := net.Dial("tcp", addr)
		if err != nil {
			_, _ = conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}

		defer upstream.Close()

		_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

		go func() {
			_, _ = io.Copy(upstream, br)
			_ = upstream.(*net.TCPConn).CloseWrite()
		}()

		_, _ = io.Copy(conn, upstream)
	})
}

func TestForwardProxy_GoesThroughASOCKS5Via(t *testing.T) {
	// The transport the proxy makes takes a socks5 Via, as http.Transport does, for the requests it sends on. Tunnels
	// go through it too.
	target := okTarget(t)
	echo := tcpHost(t, echoConn)

	dialed := make(chan string, 2)
	via := socks5Host(t, dialed)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: "socks5://" + via}))
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := proxyClient(t, m).Get(target.URL + "/x")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "ok", string(body))
	require.Equal(t, target.Listener.Addr().String(), <-dialed)

	conn, br := openTunnel(t, hostOf(t, m), echo, "")

	_, err = io.WriteString(conn, "through socks\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "through socks\n", line)
	require.Equal(t, echo, <-dialed)
}

func TestForwardProxy_ATunnelThroughSOCKS5LetsEachSideFinish(t *testing.T) {
	// A SOCKS5 connection can't end only what it sends, so a tunnel through a socks5 Via closed it whole when the
	// client ended its side, and cut the host's answer. The connection beneath ends its side instead.
	host := tcpHost(t, func(conn net.Conn) {
		got, _ := io.ReadAll(conn)
		_, _ = fmt.Fprintf(conn, "got %q", got)
	})

	dialed := make(chan string, 1)
	via := socks5Host(t, dialed)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: "socks5://" + via}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	_, err := io.WriteString(conn, "question")
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	answer, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Equal(t, `got "question"`, string(answer))
	require.Equal(t, host, <-dialed)
}
