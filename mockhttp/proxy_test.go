package mockhttp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

// socks5Host starts a SOCKS5 proxy, which sends the address of each connection it opens to dialed. With user, it
// takes only that user's name and password; without, it takes no authentication.
func socks5Host(t *testing.T, dialed chan<- string, user *url.Userinfo) string {
	t.Helper()

	return tcpHost(t, func(conn net.Conn) {
		br := bufio.NewReader(conn)

		// The greeting: the version, then the methods the client offers. No authentication is method 0, and a name
		// and a password method 2.
		greeting := make([]byte, 2)
		if _, err := io.ReadFull(br, greeting); err != nil {
			return
		}

		if _, err := io.ReadFull(br, make([]byte, greeting[1])); err != nil {
			return
		}

		if user == nil {
			_, _ = conn.Write([]byte{5, 0})
		} else {
			_, _ = conn.Write([]byte{5, 2})

			// RFC 1929: the version, then the name and the password, each after its length.
			head := make([]byte, 2)
			if _, err := io.ReadFull(br, head); err != nil {
				return
			}

			name := make([]byte, head[1])
			_, _ = io.ReadFull(br, name)
			n, _ := br.ReadByte()
			password := make([]byte, n)
			_, _ = io.ReadFull(br, password)

			if want, _ := user.Password(); string(name) != user.Username() || string(password) != want {
				_, _ = conn.Write([]byte{1, 1})
				return
			}

			_, _ = conn.Write([]byte{1, 0})
		}

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
	via := socks5Host(t, dialed, nil)

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
	via := socks5Host(t, dialed, nil)

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

func TestForwardProxy_GoesThroughASOCKS5ViaWithCredentials(t *testing.T) {
	// A socks5 Via's user info is its credentials: the tunnel opens with the right ones, and fails, saying why, with
	// others.
	echo := tcpHost(t, echoConn)

	dialed := make(chan string, 1)
	via := socks5Host(t, dialed, url.UserPassword("user", "pass"))

	t.Run("the right ones", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: "socks5://user:pass@" + via}))
		m.MustStart()
		t.Cleanup(m.Close)

		conn, br := openTunnel(t, hostOf(t, m), echo, "")

		_, err := io.WriteString(conn, "authenticated\n")
		require.NoError(t, err)

		line, err := br.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "authenticated\n", line)
		require.Equal(t, echo, <-dialed)
	})

	t.Run("others", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: "socks5://user:wrong@" + via}))
		m.MustStart()
		t.Cleanup(m.Close)

		res, body := connect(t, m, echo)
		require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
		require.Contains(t, body, "through socks5://user:xxxxx@"+via)
		require.Contains(t, body, "authentication failed")
		require.NotContains(t, body, "wrong", "the error shows the password")
	})
}

// connect sends a CONNECT to host through m, and returns its answer, which isn't a 200, and the answer's body.
func connect(t *testing.T, m *Server, host string) (*http.Response, string) {
	t.Helper()

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res, string(body)
}

// closedAddr returns the address of a TCP server that is gone: dialing it is refused.
func closedAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := l.Addr().String()
	require.NoError(t, l.Close())

	return addr
}

func TestForwardProxy_AViaThatFailsFailsTheTunnel(t *testing.T) {
	// A tunnel through a Via that fails to open one isn't opened: the client gets the server's answer to a failure,
	// which names the Via and says what went wrong, within the proxy's timeout when the Via never answers.
	host := tcpHost(t, echoConn)

	tests := []struct {
		name string
		via  string
		want string
	}{
		{name: "a Via that is down", via: "http://" + closedAddr(t), want: "connection refused"},
		{name: "a socks5 Via that is down", via: "socks5://" + closedAddr(t), want: "connection refused"},
		{
			name: "a Via that never answers",
			via:  "http://" + tcpHost(t, func(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) }),
			want: "i/o timeout",
		},
		{
			name: "a Via that drops the CONNECT",
			via: "http://" + tcpHost(t, func(conn net.Conn) {
				_, _ = http.ReadRequest(bufio.NewReader(conn))
			}),
			want: "EOF",
		},
		{
			name: "an https Via that doesn't speak TLS",
			via: "https://" + tcpHost(t, func(conn net.Conn) {
				_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\n\r\n")
				_, _ = io.Copy(io.Discard, conn)
			}),
			want: "tls: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: tt.via, Timeout: 300 * time.Millisecond}))
			m.MustStart()
			t.Cleanup(m.Close)

			res, body := connect(t, m, host)
			require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
			require.Equal(t, vlatte.FailureReply, res.Header.Get(HeaderVlatteError))
			require.Contains(t, body, "proxy: opening a tunnel to "+host+" through "+tt.via)
			require.Contains(t, body, tt.want)
		})
	}
}

func TestForwardProxy_ATunnelGoesThroughAnHTTPSVia(t *testing.T) {
	// An https Via is spoken to over TLS, verified as the proxy's ssl_verify says: the CONNECT and the tunnel go
	// through that connection.
	host := tcpHost(t, echoConn)

	via := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		defer upstream.Close()

		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}

		defer conn.Close()

		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")

		go func() {
			_, _ = io.Copy(upstream, rw)
			_ = upstream.(*net.TCPConn).CloseWrite()
		}()

		_, _ = io.Copy(conn, upstream)
	}))
	t.Cleanup(via.Close)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: via.URL}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	_, err := io.WriteString(conn, "over tls\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "over tls\n", line)
}

func TestForwardProxy_ARequestThroughItselfFails(t *testing.T) {
	// A Via that is the server itself sent each request in absolute form back to the server, which sent it on again,
	// without end. The request that comes back too often fails as a loop, and the client gets that answer.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := MustNew(Setup().Silent().Listener(l).Proxy(&ProxyConfig{Via: "http://" + l.Addr().String()}))
	m.MustStart()
	t.Cleanup(m.Close)

	res, err := proxyClient(t, m).Get("http://example.com/x")
	require.NoError(t, err, "the request went round without end")

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
	require.Contains(t, string(body), errProxyLoop.Error())
}

func TestForwardProxy_AConnectWhileTheServerClosesOpensNoTunnel(t *testing.T) {
	// A CONNECT that comes while the server closes would open a tunnel that Close no longer ends: its connections are
	// closed instead, the client's without an answer.
	hostClosed := make(chan struct{})
	host := tcpHost(t, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
		close(hostClosed)
	})

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	// What Close does once the server stops taking requests.
	m.tunnels.closeAll()

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
	require.NoError(t, err)

	got, err := io.ReadAll(conn)
	require.NoError(t, err)
	require.Empty(t, got, "the client got an answer")

	select {
	case <-hostClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("the host's connection stayed open")
	}
}

var errBroken = errors.New("broken")

// brokenConn is a connection whose reads, or writes, fail with errBroken, as those of a connection that broke do.
type brokenConn struct {
	net.Conn

	reads  bool
	writes bool
}

func (c *brokenConn) Read(p []byte) (int, error) {
	if c.reads {
		return 0, errBroken
	}

	return c.Conn.Read(p)
}

func (c *brokenConn) Write(p []byte) (int, error) {
	if c.writes {
		return 0, errBroken
	}

	return c.Conn.Write(p)
}

// breakingWriter is the writer of a middleware, which hands its connection over as a brokenConn.
type breakingWriter struct {
	http.ResponseWriter

	reads  bool
	writes bool
}

func (w breakingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, _, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}

	broken := &brokenConn{Conn: conn, reads: w.reads, writes: w.writes}

	return broken, bufio.NewReadWriter(bufio.NewReader(broken), bufio.NewWriter(broken)), nil
}

// breaking returns a middleware whose connections, once handed over, break: their reads fail, or their writes.
func breaking(reads, writes bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(breakingWriter{ResponseWriter: w, reads: reads, writes: writes}, r)
		})
	}
}

func TestForwardProxy_ATunnelThatBreaksIsAWarning(t *testing.T) {
	// A client's connection that breaks once it is handed over to a tunnel can't get the server's answer to a
	// failure: the log warns, saying why, and both connections close.
	tests := []struct {
		name   string
		reads  bool
		writes bool
		want   string
	}{
		{name: "before the answer", writes: true, want: "http: proxy: answering the CONNECT to %s: "},
		{name: "within the tunnel", reads: true, want: "http: proxy: tunnel to %s: "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostClosed := make(chan struct{})
			host := tcpHost(t, func(conn net.Conn) {
				_, _ = io.Copy(io.Discard, conn)
				close(hostClosed)
			})

			logs := &lockedBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).LogPretty(false).Proxy().Use(breaking(tt.reads, tt.writes)))
			m.MustStart()
			t.Cleanup(m.Close)

			conn, err := net.Dial("tcp", hostOf(t, m))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

			_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
			require.NoError(t, err)

			_, err = io.ReadAll(conn)
			require.NoError(t, err, "the client's connection stayed open")

			select {
			case <-hostClosed:
			case <-time.After(5 * time.Second):
				t.Fatal("the host's connection stayed open")
			}

			// The error of a copy names the connections: it is the cause that counts.
			require.Eventually(t, func() bool {
				return strings.Contains(logs.String(), fmt.Sprintf(tt.want, host)) && strings.Contains(logs.String(), "broken")
			}, 2*time.Second, 10*time.Millisecond)
			require.Contains(t, logs.String(), `"level":"warn"`)
		})
	}
}

func TestForwardProxy_VerifiesTheHostsWhenAsked(t *testing.T) {
	// A request in absolute form for an https host is sent on over TLS by the proxy. With ssl_verify, a host whose
	// certificate the server doesn't trust fails it; without, the default, it goes through.
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "host")
	}))
	t.Cleanup(host.Close)

	for _, verify := range []bool{true, false} {
		t.Run(fmt.Sprint("ssl_verify ", verify), func(t *testing.T) {
			m := MustNew(Setup().Silent().Proxy(&ProxyConfig{SSLVerify: verify}))
			m.MustStart()
			t.Cleanup(m.Close)

			conn, err := net.Dial("tcp", hostOf(t, m))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

			_, err = fmt.Fprintf(conn, "GET %s/x HTTP/1.1\r\nHost: %s\r\n\r\n", host.URL, host.Listener.Addr())
			require.NoError(t, err)

			res, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			if verify {
				require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
				require.Contains(t, string(body), "certificate")
			} else {
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, "host", string(body))
			}
		})
	}
}

// switchingTarget starts a target that switches to protocol when asked to switch to "echo", then writes back what it
// reads.
func switchingTarget(t *testing.T, protocol string) *httptest.Server {
	t.Helper()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if upgradeType(r.Header) != "echo" {
			http.Error(w, "expected a switch to echo", http.StatusBadRequest)
			return
		}

		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}

		defer conn.Close()

		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\nX-Target: yes\r\n\r\n", protocol)
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}))
	t.Cleanup(target.Close)

	return target
}

// switchThrough sends a request for target, a path or an absolute URL, that asks to switch to "echo", to m, and returns
// the connection, the reader of what comes back past the answer, and the answer.
func switchThrough(t *testing.T, m *Server, target string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", target, hostOf(t, m))
	require.NoError(t, err)

	br := bufio.NewReader(conn)

	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	require.NoError(t, err)

	return conn, br, res
}

func TestProxy_PassesProtocolSwitchesOn(t *testing.T) {
	// #69: a request that asks to switch protocols, as a WebSocket handshake does, lost its Upgrade and Connection,
	// which are hop-by-hop, and a 101 couldn't go back to the client. Proxy replies and the forward proxy pass the
	// switch on, then pipe the client's connection to the target's. The mock counts the call once the switch is done,
	// while the connection stays open.
	target := switchingTarget(t, "echo")

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Getf("/ws").Reply(From(target.URL)))

	for name, path := range map[string]string{"a proxy reply": "/ws", "the forward proxy": target.URL + "/forward"} {
		t.Run(name, func(t *testing.T) {
			conn, br, res := switchThrough(t, m, path)

			require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
			require.Equal(t, "echo", res.Header.Get(HeaderUpgrade))
			require.Equal(t, "yes", res.Header.Get("X-Target"))
			require.Equal(t, "true", res.Header.Get(vlatte.ProxiedHeader))
			require.NotEmpty(t, res.Header.Get(vlatte.RequestIDHeader))

			_, err := io.WriteString(conn, "switched\n")
			require.NoError(t, err)

			line, err := br.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "switched\n", line)
		})
	}

	require.Equal(t, int64(1), scoped.Hits(), "the mock counts the call while its connection is open")
}

func TestProxy_AMockThatCantFollowASwitchDoesntAskForOne(t *testing.T) {
	// Response mappers change a body: a connection that switched protocols has none. A mock with them doesn't ask its
	// target to switch, so the target answers as to any request, and they apply to that answer: the target doesn't
	// open a connection the proxy would drop. A target that switches to another protocol than the client asked for
	// fails.
	echo := switchingTarget(t, "echo")
	other := switchingTarget(t, "other")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(
		Getf("/mapped").Map(func(_ *RequestValues, res *Response) error { res.Body = append(res.Body, '?'); return nil }).Reply(From(echo.URL)),
		Getf("/other").Reply(From(other.URL)),
	)

	for path, want := range map[string]struct {
		status int
		body   string
	}{
		"/mapped": {http.StatusBadRequest, "expected a switch to echo\n?"},
		"/other":  {m.Config().ErrorStatusCode, `switched to the protocol "other", not "echo"`},
	} {
		_, _, res := switchThrough(t, m, path)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, want.status, res.StatusCode, path)
		require.Contains(t, string(body), want.body, path)
	}
}

func TestProxy_ASwitchedConnectionPassesTheStepsOfTheMock(t *testing.T) {
	// Faults apply to a WebSocket as to any connection: what the target sends the client passes the steps of the mock,
	// and what the client sends the target its upstream steps.
	echo := switchingTarget(t, "echo")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Getf("/ws").Step(appendStep("!")).UpstreamStep(appendStep("?")).Reply(From(echo.URL)))

	conn, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	_, err := io.WriteString(conn, "hi")
	require.NoError(t, err)

	got := make([]byte, len("hi?!"))
	_, err = io.ReadFull(br, got)
	require.NoError(t, err)
	require.Equal(t, "hi?!", string(got))
	require.EqualValues(t, 1, scoped.Hits())
}

func TestProxy_TheWireStepsOfASwitchSeeEveryByte(t *testing.T) {
	// A mock that answers on the wire passes the whole connection through its wire steps: the 101, then what the
	// target sends, after the steps of the mock.
	echo := switchingTarget(t, "echo")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	wire := make(chan []byte, 1)
	m.MustMock(Getf("/ws").Step(appendStep("!")).WireStep(wireLogStep{got: wire}).Reply(From(echo.URL)))

	conn, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
	require.Equal(t, "echo", res.Header.Get(HeaderUpgrade))

	_, err := io.WriteString(conn, "hi")
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	rest, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Equal(t, "hi!", string(rest))

	seen := <-wire
	require.True(t, bytes.HasPrefix(seen, []byte("HTTP/1.1 101 Switching Protocols\r\n")), "%q", seen)
	require.True(t, bytes.HasSuffix(seen, []byte("\r\n\r\nhi!")), "%q", seen)
}

func TestProxy_AStepThatAbortsASwitchedConnectionCutsBothSides(t *testing.T) {
	// A step cuts a WebSocket as it cuts any answer, after the bytes it let through, or resets it: the client gets the
	// close, or the reset, it would get from a server that fails.
	tests := []struct {
		name  string
		err   error
		reset bool
	}{
		{name: "an abort", err: vlatte.ErrAbort},
		{name: "a reset", err: vlatte.ErrReset, reset: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			echo := switchingTarget(t, "echo")

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			scoped := m.MustMock(Getf("/ws").Step(limitStep{n: 2, err: tc.err}).Reply(From(echo.URL)))

			conn, br, res := switchThrough(t, m, "/ws")
			require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

			_, err := io.WriteString(conn, "hello")
			require.NoError(t, err)

			got, err := io.ReadAll(br)
			if tc.reset {
				require.ErrorIs(t, err, syscall.ECONNRESET)
			} else {
				require.NoError(t, err)
				require.Equal(t, "he", string(got))
			}

			require.EqualValues(t, 1, scoped.Hits())
		})
	}
}

func TestProxy_AStepThatFailsAsASideEndsCutsBothSides(t *testing.T) {
	// A side that ends what it sends ends its steps: one that fails there, such as to reset once the client is done
	// sending, cuts both sides, rather than leave the other going on, which waits for an end that doesn't come.
	tests := []struct {
		name  string
		err   error
		reset bool
	}{
		{name: "a failure", err: errors.New("step: failed")},
		{name: "a reset", err: vlatte.ErrReset, reset: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			echo := switchingTarget(t, "echo")

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			closed := make(chan error, 1)
			m.MustMock(Getf("/ws").UpstreamStep(closeStep{closed: closed, err: tc.err}).Reply(From(echo.URL)))

			conn, br, res := switchThrough(t, m, "/ws")
			require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

			_, err := io.WriteString(conn, "hi")
			require.NoError(t, err)

			got := make([]byte, len("hi"))
			_, err = io.ReadFull(br, got)
			require.NoError(t, err)
			require.Equal(t, "hi", string(got))

			require.NoError(t, conn.(*net.TCPConn).CloseWrite())

			rest, err := io.ReadAll(br)
			if tc.reset {
				require.ErrorIs(t, err, syscall.ECONNRESET)
			} else {
				require.NoError(t, err)
				require.Empty(t, rest)
			}

			require.NoError(t, <-closed)
		})
	}
}

func TestProxy_AStepCanCutAnIdleSwitchedConnection(t *testing.T) {
	// A timeout on a WebSocket that carries nothing: no write comes, so only the flow's abort can cut it.
	echo := switchingTarget(t, "echo")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	release := make(chan struct{})
	m.MustMock(Getf("/ws").Step(abortOn{release: release, err: errors.New("step: idle")}).Reply(From(echo.URL)))

	_, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	close(release)

	got, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestProxy_AStepThatCutsTheAnswerCutsTheSwitch(t *testing.T) {
	// The answer of a switch ends with its head: a step that cuts it there leaves the client without a switch.
	echo := switchingTarget(t, "echo")

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	closed := make(chan error, 2)
	scoped := m.MustMock(Getf("/ws").Step(closeStep{closed: closed, err: vlatte.ErrAbort}).Reply(From(echo.URL)))

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", hostOf(t, m))
	require.NoError(t, err)

	got, err := io.ReadAll(conn)
	require.NoError(t, err)
	require.Empty(t, got)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestProxy_AWireStepThatCutsThe101CutsTheSwitch(t *testing.T) {
	// The wire steps see the 101 as any head: one that cuts it leaves the client without a switch, closed or reset, as
	// it asked. The cut is the mock's answer: the call counts.
	tests := []struct {
		name  string
		err   error
		reset bool
	}{
		{name: "an abort", err: vlatte.ErrAbort},
		{name: "a reset", err: vlatte.ErrReset, reset: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			echo := switchingTarget(t, "echo")

			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)

			cut := vlatte.StepFunc(func(context.Context, []byte, vlatte.Stage) (int, error) { return 0, tc.err })
			scoped := m.MustMock(Getf("/ws").WireStep(cut).Reply(From(echo.URL)))

			conn, err := net.Dial("tcp", hostOf(t, m))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

			_, err = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", hostOf(t, m))
			require.NoError(t, err)

			got, err := io.ReadAll(conn)
			if tc.reset {
				require.ErrorIs(t, err, syscall.ECONNRESET)
			} else {
				require.NoError(t, err)
			}

			require.Empty(t, got)
			require.EqualValues(t, 1, scoped.Hits())
		})
	}
}

func TestProxy_ATargetThatEndsWhatItSendsLeavesTheClientSending(t *testing.T) {
	// A side that ends what it sends has the other side go on until its own end, through the steps of each direction:
	// the end of what the target sends, through the wire steps, doesn't end what the client sends.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}

		defer conn.Close()

		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\nhello")
		_ = rw.Flush()
		_ = conn.(*net.TCPConn).CloseWrite()

		b, _ := io.ReadAll(rw)
		got <- string(b)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/ws").WireStep(vlatte.StepFunc(passWire)).UpstreamStep(appendStep("?")).Reply(From(target.URL)))

	conn, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	hello, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Equal(t, "hello", string(hello))

	_, err = io.WriteString(conn, "ping")
	require.NoError(t, err)
	require.NoError(t, conn.(*net.TCPConn).CloseWrite())

	require.Equal(t, "ping?", <-got)
}

func TestProxy_AClientMayOfferSeveralProtocols(t *testing.T) {
	// A client may offer several protocols, of which the target picks one. h2c asks for HTTP/2 on the client's own
	// connection, which the proxy doesn't switch, so it isn't asked on.
	got := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get(HeaderUpgrade)

		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}

		defer conn.Close()

		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/ws").Reply(From(target.URL)))

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: h2c, other, echo\r\n\r\n", hostOf(t, m))
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
	require.Equal(t, "echo", res.Header.Get(HeaderUpgrade))
	require.Equal(t, "other, echo", <-got)
}

func TestProxy_CloseEndsConnectionsThatSwitchedProtocols(t *testing.T) {
	// The server doesn't track a connection it handed over: Close ends those that switched protocols too.
	target := switchingTarget(t, "echo")

	m := MustNew(Setup().Silent())
	m.MustStart()
	m.MustMock(Getf("/ws").Reply(From(target.URL)))

	_, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	m.Close()

	_, err := br.ReadString('\n')
	require.ErrorIs(t, err, io.EOF, "the connection is still open")
}

func TestProxy_ASwitchedConnectionFlowsWhileCallbacksRun(t *testing.T) {
	// The pipe waited for the mock's callbacks and events, such as a webhook with a delay: the client got its 101,
	// and nothing went through until they were done. It flows at once.
	target := switchingTarget(t, "echo")

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/ws").
		Callback(func(context.Context, vlatte.CallbackInput) error {
			<-release
			return nil
		}).
		Reply(From(target.URL)))

	conn, br, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	_, err := io.WriteString(conn, "while the callback runs\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err, "nothing went through while the callback ran")
	require.Equal(t, "while the callback runs\n", line)
}

// endingSwitchTarget starts a target that switches to echo and writes back what it reads: ended closes once the
// connection that switched ends.
func endingSwitchTarget(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()

	ended := make(chan struct{})

	var once sync.Once

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}

		defer once.Do(func() { close(ended) })
		defer conn.Close()

		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}))
	t.Cleanup(target.Close)

	return target, ended
}

// requireEnded fails t unless ended closes soon.
func requireEnded(t *testing.T, ended <-chan struct{}, msg string) {
	t.Helper()

	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal(msg)
	}
}

func TestProxy_ASwitchThatCantTakeTheConnectionOverFails(t *testing.T) {
	// A target that switched protocols has its connection piped to the client's, which the server must take over:
	// behind a writer that can't hand it over, the client gets the server's answer to a failure, saying why, and the
	// target's connection closes, through a proxy reply as through the forward proxy.
	for name, path := range map[string]func(target string) string{
		"a proxy reply":     func(string) string { return "/ws" },
		"the forward proxy": func(target string) string { return target + "/forward" },
	} {
		t.Run(name, func(t *testing.T) {
			target, ended := endingSwitchTarget(t)

			m := MustNew(Setup().Silent().Proxy().Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(opaqueWriter{w}, r) })
			}))
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(Getf("/ws").Reply(From(target.URL)))

			_, _, res := switchThrough(t, m, path(target.URL))

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
			require.Contains(t, string(body), "switching protocols needs an HTTP/1.1 connection, and a response writer that can hijack it")
			requireEnded(t, ended, "the target's connection stayed open")
		})
	}
}

func TestProxy_ASwitchWhileTheServerClosesIsDropped(t *testing.T) {
	// A switch that comes while the server closes would pipe connections that Close no longer ends: both are closed
	// instead, the client's without an answer.
	target, ended := endingSwitchTarget(t)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/ws").Reply(From(target.URL)))

	// What Close does once the server stops taking requests.
	m.tunnels.closeAll()

	conn, err := net.Dial("tcp", hostOf(t, m))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", hostOf(t, m))
	require.NoError(t, err)

	got, err := io.ReadAll(conn)
	require.NoError(t, err)
	require.Empty(t, got, "the client got an answer")
	requireEnded(t, ended, "the target's connection stayed open")
}

func TestProxy_ASwitchedConnectionThatBreaksIsAWarning(t *testing.T) {
	// A client's connection that breaks once the server took it over can't get the server's answer to a failure: the
	// log warns, saying why, and the target's connection closes.
	tests := []struct {
		name   string
		reads  bool
		writes bool
		wire   bool
		want   string
	}{
		{name: "before the 101", writes: true, want: "http: answering the switch of protocols: "},
		{name: "before the 101 through the wire steps", writes: true, wire: true, want: "http: answering the switch of protocols: "},
		{name: "once switched", reads: true, want: "http: connection that switched protocols: "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, ended := endingSwitchTarget(t)

			logs := &lockedBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).LogPretty(false).Use(breaking(tt.reads, tt.writes)))
			m.MustStart()
			t.Cleanup(m.Close)

			mock := Getf("/ws")
			if tt.wire {
				mock = mock.WireStep(vlatte.StepFunc(passWire))
			}

			m.MustMock(mock.Reply(From(target.URL)))

			conn, err := net.Dial("tcp", hostOf(t, m))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

			_, err = fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", hostOf(t, m))
			require.NoError(t, err)

			_, err = io.ReadAll(conn)
			require.NoError(t, err, "the client's connection stayed open")
			requireEnded(t, ended, "the target's connection stayed open")

			// The error of a copy names the connections: it is the cause that counts.
			require.Eventually(t, func() bool {
				return strings.Contains(logs.String(), tt.want) && strings.Contains(logs.String(), "broken")
			}, 2*time.Second, 10*time.Millisecond)
			require.Contains(t, logs.String(), `"level":"warn"`)
		})
	}
}

func TestForwardProxy_ATunnelDialsWithinTheTransportsLimits(t *testing.T) {
	// A ProxyConfig without Timeout has 0, which bounded nothing in a tunnel: it dialed its host, or its Via, with no
	// limit but the system's, a couple of minutes, and waited on an https Via's handshake for ever. Tunnels dial
	// within the limits of the transport the proxy makes, 30s to connect and 10s for a handshake, which the test
	// shortens.
	host := tcpHost(t, echoConn)

	t.Run("connecting", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{}))
		m.proxy.dialer.Timeout = time.Nanosecond
		m.MustStart()
		t.Cleanup(m.Close)

		res, body := connect(t, m, host)
		require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
		require.Contains(t, body, "proxy: opening a tunnel to "+host)
		require.Contains(t, body, "i/o timeout")
	})

	t.Run("an https Via's handshake", func(t *testing.T) {
		via := tcpHost(t, func(conn net.Conn) {
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = io.Copy(io.Discard, conn)
		})

		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Via: "https://" + via}))
		m.proxy.handshakeTimeout = 100 * time.Millisecond
		m.MustStart()
		t.Cleanup(m.Close)

		res, body := connect(t, m, host)
		require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
		require.Contains(t, body, "proxy: opening a tunnel to "+host+" through https://"+via)
	})
}

func TestProxyAll_AnswersWhatNoOtherMockMatches(t *testing.T) {
	// As WireMock's proxy-all, ProxyAll proxies what no other mock matches: it comes last, whatever the priority of
	// the others. It leaves the forward proxy's requests alone, which name their host: a request in absolute form goes
	// to its host, and a CONNECT opens its tunnel. Its description loads back as the same mock, and a mock file's
	// proxy_all makes one: a description that held a plain proxy loaded back as a mock of any request, which took the
	// tunnels and the forward proxy's requests too.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	host := okTarget(t)
	echo := tcpHost(t, echoConn)

	described := func(t *testing.T) vlatte.Builder[*Mock, *Server] {
		mock, err := ProxyAll(From(target.URL)).Build(MustNew(Setup().Silent()))
		require.NoError(t, err)

		desc := mock.Describe().Props.(map[string]any)
		require.Contains(t, desc, "proxy_all")
		require.NotContains(t, desc, "proxy")

		content, err := mock.MarshalJSON()
		require.NoError(t, err)

		return FromBytes(content, "json")
	}

	for name, proxyAll := range map[string]func(t *testing.T) vlatte.Builder[*Mock, *Server]{
		"ProxyAll":        func(*testing.T) vlatte.Builder[*Mock, *Server] { return ProxyAll(From(target.URL)) },
		"its description": described,
		"a mock file's mock": func(*testing.T) vlatte.Builder[*Mock, *Server] {
			return FromBytes([]byte("proxy_all: {target: '"+target.URL+"'}\n"), "yaml")
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := MustNew(Setup().Silent().Proxy())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(
				proxyAll(t),
				Getf("/mocked").Priority(1000).Reply(OK().PlainText("mock")),
			)

			_, body := get(t, m, "/mocked")
			require.Equal(t, "mock", body)

			_, body = get(t, m, "/other")
			require.Equal(t, "target /other", body)

			res, err := proxyClient(t, m).Get(host.URL + "/x")
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, "ok", string(b), "the forward proxy's request went to the proxy-all target")

			conn, br := openTunnel(t, hostOf(t, m), echo, "")

			_, err = io.WriteString(conn, "tunnel\n")
			require.NoError(t, err)

			line, err := br.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "tunnel\n", line)
		})
	}
}

func TestProxyAll_AMockFilesPriorityWins(t *testing.T) {
	// A mock file's proxy_all comes last, unless the file gives it a priority: then it is matched in its turn.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(
		FromBytes([]byte("priority: 1\nproxy_all: {target: '"+target.URL+"'}\n"), "yaml"),
		Getf("/mocked").Priority(1000).Reply(OK().PlainText("mock")),
	)

	_, body := get(t, m, "/mocked")
	require.Equal(t, "target /mocked", body)
}

func TestProxyAll_APriorityOf0SurvivesDescribe(t *testing.T) {
	// A proxy_all mock given a priority of 0 is matched among the priority-0 mocks, but its description left 0 out, as
	// for any mock, which has 0 by default: loaded back, it came last again, at math.MaxInt, and another mock won. Its
	// description keeps it.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	file := []byte("priority: 0\nproxy_all: {target: '" + target.URL + "'}\n")

	described := func(t *testing.T, b vlatte.Builder[*Mock, *Server]) vlatte.Builder[*Mock, *Server] {
		t.Helper()

		mock, err := b.Build(MustNew(Setup().Silent()))
		require.NoError(t, err)
		require.Equal(t, 0, mock.Describe().Props.(map[string]any)["priority"])

		content, err := mock.MarshalJSON()
		require.NoError(t, err)

		return FromBytes(content, "json")
	}

	for name, proxyAll := range map[string]func(t *testing.T) vlatte.Builder[*Mock, *Server]{
		"ProxyAll": func(*testing.T) vlatte.Builder[*Mock, *Server] { return ProxyAll(From(target.URL)).Priority(0) },
		"its description": func(t *testing.T) vlatte.Builder[*Mock, *Server] {
			return described(t, ProxyAll(From(target.URL)).Priority(0))
		},
		"a mock file's mock":     func(*testing.T) vlatte.Builder[*Mock, *Server] { return FromBytes(file, "yaml") },
		"the file's description": func(t *testing.T) vlatte.Builder[*Mock, *Server] { return described(t, FromBytes(file, "yaml")) },
	} {
		t.Run(name, func(t *testing.T) {
			m := MustNew(Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(proxyAll(t), Getf("/x").Priority(1).Reply(OK().PlainText("mock")))

			_, body := get(t, m, "/x")
			require.Equal(t, "target /x", body, "the proxy_all mock wasn't matched before the mock of priority 1")
		})
	}
}

func TestProxyAll_AMockFilesProxyAllIsTheOnlyReply(t *testing.T) {
	// proxy_all is a reply: a mock file can't give it next to another one.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	for _, other := range []string{"response: {status: 200}", "proxy: {target: 'http://a.local'}"} {
		_, err := m.Mock(FromBytes([]byte("proxy_all: {target: 'http://b.local'}\n"+other+"\n"), "yaml"))
		require.ErrorContains(t, err, "proxy_all", other)
	}
}

func TestProxyAll_FromTheSettings(t *testing.T) {
	// WireMock's --proxy-all: the setting proxy_all takes the keys of a mock file's proxy, and answers what no mock
	// matches. It is no mock: assertions don't count it, and Reload and Clean leave it be. Without a forward proxy, a
	// request in absolute form goes to it too.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	settings := writeConfigFile(t, t.TempDir(), "settings.yaml",
		"proxy_all:\n  target: '"+target.URL+"?key=secret'\n  trim_prefix: /api\n  timeout: 2s\n")

	m := MustNew(Setup().Silent().ConfigSources(ConfigFile(settings)))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/api/mocked").Reply(OK().PlainText("mock")))

	_, body := get(t, m, "/api/mocked")
	require.Equal(t, "mock", body)

	_, body = get(t, m, "/api/users")
	require.Equal(t, "target /users", body)

	m.AssertCalled(t)

	require.NoError(t, m.Reload())
	require.NoError(t, m.Clean(t.Context()))

	_, body = get(t, m, "/api/users")
	require.Equal(t, "target /users", body, "Reload or Clean dropped proxy_all")

	res, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustURL(t, m.URL()))}}).Get("http://elsewhere.local/api/x")
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "target /x", string(b), "a request in absolute form, on a server that is no forward proxy")

	require.Contains(t, m.DescribeConfig(), "Proxy All: "+target.URL+"?key=xxxxx")
}

func TestProxyAll_TheSettingsNeedATarget(t *testing.T) {
	settings := writeConfigFile(t, t.TempDir(), "settings.yaml", "proxy_all:\n  trim_prefix: /api\n")

	_, err := New(Setup().Silent().ConfigSources(ConfigFile(settings)))
	require.ErrorContains(t, err, "proxy_all is set but proxy_all.target is not")
	require.ErrorContains(t, err, "settings.yaml")
}

func TestProxyAll_WithoutAForwardProxy(t *testing.T) {
	// ProxyAll left alone every request in absolute form, which only a forward proxy sends on: without one, such a
	// request matched nothing. A nil reply fails the mock, rather than panic when it is built.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(ProxyAll(From(target.URL)))

	res, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(mustURL(t, m.URL()))}}).Get("http://elsewhere.local/x")
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "target /x", string(b))

	_, err = m.Mock(ProxyAll(nil))
	require.ErrorContains(t, err, "ProxyAll: the reply is nil")
}

// mustURL parses raw.
func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	return u
}

func TestProxyAll_GivenAnotherReplyDescribesThatReply(t *testing.T) {
	// A ProxyAll mock given another reply holds no proxy reply: its description has no proxy_all, but the reply it has.
	mock, err := ProxyAll(From("http://target.local")).Reply(OK()).Build(MustNew(Setup().Silent()))
	require.NoError(t, err)

	desc := mock.Describe().Props.(map[string]any)
	require.NotContains(t, desc, "proxy_all")
	require.Contains(t, desc, "response")
}

func TestProxyAll_TheSettingsFailWhatTheCallersTransportWouldIgnore(t *testing.T) {
	// #67: proxy_all.ssl_verify, with a transport of the caller's, which decides on its own whether it verifies, fails
	// New, as a mock's ssl_verify fails the mock.
	_, err := New(Setup().Silent().Transport(newCountingTransport(t)).ConfigSources(ConfigArgs([]string{
		"--proxy-all-target", "http://target.local", "--proxy-all-ssl-verify",
	})))
	require.ErrorContains(t, err, "server: proxy_all: ")
	require.ErrorContains(t, err, "ssl_verify has no effect with a transport of the caller's")
}

func TestProxyAll_TheSettingsShapeTheRequest(t *testing.T) {
	// The setting proxy_all takes the keys of a mock file's proxy that flags and variables can give: the target gets
	// the request without the headers to remove, trimmed, with the client's details and the Host it asked for. Its
	// target can come from the environment.
	type sent struct {
		host   string
		path   string
		header http.Header
	}

	got := make(chan sent, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got <- sent{host: r.Host, path: r.URL.Path, header: r.Header.Clone()}
	}))
	t.Cleanup(target.Close)

	settings := writeConfigFile(t, t.TempDir(), "settings.yaml", "proxy_all:\n"+
		"  remove_request_headers: [X-Removed]\n  trim_prefix: /api\n  x_forwarded: true\n  preserve_host: true\n")
	t.Setenv("VLATTE_PROXY_ALL_TARGET", target.URL)

	m := MustNew(Setup().Silent().ConfigSources(ConfigFile(settings), ConfigEnv()))
	m.MustStart()
	t.Cleanup(m.Close)

	req, err := http.NewRequest(http.MethodGet, m.URL("/api/x"), nil)
	require.NoError(t, err)
	req.Header.Set("X-Removed", "r")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	s := <-got
	require.Equal(t, hostOf(t, m), s.host)
	require.Equal(t, "/x", s.path)
	require.Empty(t, s.header.Get("X-Removed"))
	require.Equal(t, hostOf(t, m), s.header.Get("X-Forwarded-Host"))
	require.Equal(t, "127.0.0.1", s.header.Get("X-Forwarded-For"))
}

func TestProxyAll_PassesProtocolSwitchesOn(t *testing.T) {
	// A switch of protocols that no mock matches goes to the proxy-all target, as through any proxy reply, whether a
	// ProxyAll mock or the setting proxy_all answers it.
	target := switchingTarget(t, "echo")

	for name, m := range map[string]*Server{
		"a ProxyAll mock": MustNew(Setup().Silent()),
		"the setting":     MustNew(Setup().Silent().ConfigSources(ConfigArgs([]string{"--proxy-all-target", target.URL}))),
	} {
		t.Run(name, func(t *testing.T) {
			m.MustStart()
			t.Cleanup(m.Close)

			if name == "a ProxyAll mock" {
				m.MustMock(ProxyAll(From(target.URL)))
			}

			conn, br, res := switchThrough(t, m, "/ws")
			require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

			_, err := io.WriteString(conn, "switched\n")
			require.NoError(t, err)

			line, err := br.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "switched\n", line)
		})
	}
}

// interceptClient returns a client that sends its requests through m, which intercepts their TLS, and trusts its CA.
func interceptClient(t *testing.T, m *Server) *http.Client {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(m.ProxyCA())

	transport := &http.Transport{Proxy: http.ProxyURL(mustURL(t, m.URL())), TLSClientConfig: &tls.Config{RootCAs: pool}}
	t.Cleanup(transport.CloseIdleConnections)

	return &http.Client{Transport: transport}
}

func TestForwardProxy_InterceptsHTTPS(t *testing.T) {
	// WireMock's HTTPS browser proxying: the TLS in a CONNECT tunnel hid its requests, so mocks never saw them. With
	// Intercept, the proxy serves that TLS itself, with a certificate for the host signed by its CA, which the client
	// trusts: mocks answer the requests within, and the proxy sends the rest on to the host.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "target "+r.URL.Path)
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)
	scoped := m.MustMock(Getf("/mocked").Reply(OK().PlainText("mock")))

	client := interceptClient(t, m)

	for path, want := range map[string]string{"/mocked": "mock", "/other": "target /other"} {
		res, err := client.Get(target.URL + path)
		require.NoError(t, err, path)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, want, string(body), path)
	}

	scoped.AssertCalled(t)
	require.Contains(t, m.DescribeConfig(), "Forward Proxy: enabled, intercepting HTTPS")
}

func TestForwardProxy_InterceptsWithTheCAItIsGiven(t *testing.T) {
	// A CA of the caller's signs the hosts' certificates, as for clients that trust it already. A certificate that is no
	// CA's fails New.
	ca := signedCA(t, "the caller's CA", x509.KeyUsageCertSign, nil)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, CA: ca}))
	t.Cleanup(m.Close)
	require.Equal(t, ca.Leaf, m.ProxyCA())

	leaf, err := tls.LoadX509KeyPair(_testCertFile, _testKeyFile)
	require.NoError(t, err)

	_, err = New(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, CA: &leaf}))
	require.ErrorContains(t, err, "is no CA's: it can't sign certificates")

	plain := MustNew(Setup().Silent().Proxy())
	t.Cleanup(plain.Close)
	require.Nil(t, plain.ProxyCA(), "a proxy that doesn't intercept has no CA")
}

func TestForwardProxy_InterceptionPipesWhatIsntTLS(t *testing.T) {
	// A client that doesn't start TLS in its tunnel, such as one of another protocol, goes to the host as it would
	// without interception.
	echo := tcpHost(t, echoConn)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)

	_, br := openTunnel(t, hostOf(t, m), echo, "plain\n")

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "plain\n", line)
}

func TestForwardProxy_CloseEndsInterceptedConnections(t *testing.T) {
	// The server serves an intercepted connection with a server of its own, which Close ends with the tunnels.
	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	m.MustMock(Getf("/mocked").Reply(OK().PlainText("mock")))

	conn, _ := openTunnel(t, hostOf(t, m), "api.local:443", "")

	pool := x509.NewCertPool()
	pool.AddCert(m.ProxyCA())

	client := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: "api.local"})
	require.NoError(t, client.Handshake())

	_, err := io.WriteString(client, "GET /mocked HTTP/1.1\r\nHost: api.local\r\n\r\n")
	require.NoError(t, err)

	br := bufio.NewReader(client)

	res, err := http.ReadResponse(br, nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "mock", string(body))

	m.Close()

	_, err = br.ReadByte()
	require.Error(t, err, "the intercepted connection is still open")
}

func TestForwardProxy_InterceptedConnectionsSwitchProtocols(t *testing.T) {
	// A request within an intercepted connection that switches protocols, such as a WebSocket to an https host, takes
	// the connection over: the interception leaves it open to the switch.
	target := httptest.NewTLSServer(switchingTarget(t, "echo").Config.Handler)
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)

	host := mustURL(t, target.URL).Host
	conn, _ := openTunnel(t, hostOf(t, m), host, "")

	pool := x509.NewCertPool()
	pool.AddCert(m.ProxyCA())

	client := tls.Client(conn, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1"})
	require.NoError(t, client.Handshake())

	_, err := fmt.Fprintf(client, "GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", host)
	require.NoError(t, err)

	br := bufio.NewReader(client)

	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	_, err = io.WriteString(client, "switched within\n")
	require.NoError(t, err)

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "switched within\n", line)
}

func TestForwardProxy_InterceptionTunnelsAHostThatSpeaksFirst(t *testing.T) {
	// A client of a protocol whose server speaks first, such as SSH or SMTP, waits for its host: an interception that
	// waited for the client's first bytes closed the connection after its handshake timeout. It goes to the host.
	host := tcpHost(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "banner\n")
		echoConn(conn)
	})

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)

	conn, br := openTunnel(t, hostOf(t, m), host, "")

	line, err := br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "banner\n", line)

	_, err = io.WriteString(conn, "after the banner\n")
	require.NoError(t, err)

	line, err = br.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "after the banner\n", line)
}

func TestForwardProxy_CloseWaitsForInterceptedRequests(t *testing.T) {
	// The backend doesn't wait for the requests of an intercepted connection, which its own server serves: Close ran on
	// while they were served, and closed their mocks and recording under them. It waits for them.
	entered, release := make(chan struct{}), make(chan struct{})

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	m.MustMock(Getf("/slow").
		Callback(func(context.Context, vlatte.CallbackInput) error {
			close(entered)
			<-release

			return nil
		}).
		Reply(OK()))

	client := interceptClient(t, m)

	go func() {
		if res, err := client.Get("https://api.local/slow"); err == nil {
			_ = res.Body.Close()
		}
	}()

	<-entered

	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("Close returned while an intercepted request was served")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close didn't return once the intercepted request was served")
	}
}

func TestForwardProxy_InterceptionTunnelsClientsThatSpeakOnlyHTTP2(t *testing.T) {
	// The interception serves HTTP/1.1: a client that offers only h2, such as a gRPC one, failed its handshake. It goes
	// to the host through a tunnel, and gets the host's own certificate and protocol. A client that offers HTTP/1.1
	// too is intercepted.
	target := httptest.NewUnstartedServer(http.NotFoundHandler())
	target.EnableHTTP2 = true
	target.StartTLS()
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)

	roots := x509.NewCertPool()
	roots.AddCert(target.Certificate())
	roots.AddCert(m.ProxyCA())

	for protocols, want := range map[string]struct {
		protocol string
		issuer   string
	}{
		"h2":          {"h2", target.Certificate().Issuer.String()},
		"h2,http/1.1": {"http/1.1", m.ProxyCA().Subject.String()},
	} {
		conn, _ := openTunnel(t, hostOf(t, m), target.Listener.Addr().String(), "")

		client := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", NextProtos: strings.Split(protocols, ",")})
		require.NoError(t, client.Handshake(), protocols)

		state := client.ConnectionState()
		require.Equal(t, want.protocol, state.NegotiatedProtocol, protocols)
		require.Equal(t, want.issuer, state.PeerCertificates[0].Issuer.String(), protocols)
	}
}

func TestForwardProxy_InterceptsFromTheSettings(t *testing.T) {
	// proxy.intercept, with the CA's files, intercepts as ProxyConfig.Intercept does: a client that trusts the CA's
	// file gets the mocks' answers to its HTTPS requests.
	dir := t.TempDir()
	caCert, caKey := writeTestCA(t, dir)

	m := MustNew(Setup().Silent().ConfigSources(ConfigArgs([]string{
		"--proxy-intercept", "--proxy-ca-cert", caCert, "--proxy-ca-key", caKey,
	})))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/mocked").Reply(OK().PlainText("mock")))

	pem, err := os.ReadFile(caCert)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(pem))

	transport := &http.Transport{Proxy: http.ProxyURL(mustURL(t, m.URL())), TLSClientConfig: &tls.Config{RootCAs: pool}}
	t.Cleanup(transport.CloseIdleConnections)

	res, err := (&http.Client{Transport: transport}).Get("https://api.example.com/mocked")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "mock", string(body))
}

func TestForwardProxy_SendsInterceptedRequestsOnWithItsSettings(t *testing.T) {
	// An intercepted request no mock matches goes on to its host as the proxy's requests do: verified when ssl_verify
	// says so, through Via, and to its own host, not to proxy_all's target.
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "host "+r.URL.Path)
	}))
	t.Cleanup(host.Close)

	elsewhere := okTarget(t)

	get := func(t *testing.T, m *Server) (int, string) {
		t.Helper()

		res, err := interceptClient(t, m).Get(host.URL + "/x")
		require.NoError(t, err)

		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res.StatusCode, string(body)
	}

	t.Run("ssl_verify", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, SSLVerify: true}))
		m.MustStart()
		t.Cleanup(m.Close)

		status, body := get(t, m)
		require.Equal(t, m.Config().ErrorStatusCode, status)
		require.Contains(t, body, "certificate")
	})

	t.Run("a Via", func(t *testing.T) {
		connects := make(chan string, 1)
		via := tcpHost(t, func(conn net.Conn) {
			br := bufio.NewReader(conn)

			req, err := http.ReadRequest(br)
			if err != nil {
				return
			}

			connects <- req.Method + " " + req.Host

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

		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, Via: "http://" + via}))
		m.MustStart()
		t.Cleanup(m.Close)

		status, body := get(t, m)
		require.Equal(t, http.StatusOK, status, body)
		require.Equal(t, "host /x", body)
		require.Equal(t, "CONNECT "+host.Listener.Addr().String(), <-connects)
	})

	t.Run("proxy_all", func(t *testing.T) {
		m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}).ConfigSources(ConfigArgs([]string{
			"--proxy-all-target", elsewhere.URL,
		})))
		m.MustStart()
		t.Cleanup(m.Close)

		status, body := get(t, m)
		require.Equal(t, http.StatusOK, status, body)
		require.Equal(t, "host /x", body)
	})
}
