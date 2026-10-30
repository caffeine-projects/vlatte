package e2e

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

var _ net.Listener = (*pipeListener)(nil)

func TestServer_APortWithoutAHostServesTheMocks(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Addr("0"))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.Getf("/tea").Reply(mockhttp.Created()))
	_, port, err := net.SplitHostPort(m.URL()[len("http://"):])
	require.NoError(t, err)
	res, err := http.Get("http://127.0.0.1:" + port + "/tea")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestServer_TLSCannotStartOnAnOccupiedPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Addr(l.Addr().String()))
	t.Cleanup(m.Close)
	require.ErrorIs(t, m.StartTLS(), syscall.EADDRINUSE)
}

func TestServer_IncompleteHeadersReleaseTheConnectionAtTheReadTimeout(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		timeout time.Duration
	}{
		{
			name:    "header timeout alone",
			args:    []string{"--read-header-timeout", "1s"},
			timeout: time.Second,
		},
		{
			name:    "header timeout defaults to read timeout",
			args:    []string{"--read-timeout", "1s"},
			timeout: time.Second,
		},
		{
			name:    "header timeout is shorter than read timeout",
			args:    []string{"--read-header-timeout", "1s", "--read-timeout", "2s"},
			timeout: time.Second,
		},
		{
			name:    "header timeout is longer than read timeout",
			args:    []string{"--read-header-timeout", "2s", "--read-timeout", "1s"},
			timeout: 2 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn, _ := startPipeServer(t, mockhttp.Setup().ConfigSources(mockhttp.ConfigArgs(tt.args)))
				start := time.Now()
				require.NoError(t, conn.SetReadDeadline(start.Add(tt.timeout+time.Second)))
				_, err := io.WriteString(conn, "GET /tea HTTP/1.1\r\nHost: example.test\r\n")
				require.NoError(t, err)

				_, err = conn.Read(make([]byte, 1))

				require.ErrorIs(t, err, io.EOF)
				require.Equal(t, tt.timeout, time.Since(start))
			})
		})
	}
}

func TestServer_IncompleteBodyStopsWaitingAtTheReadTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, m := startPipeServer(t, mockhttp.Setup().ConfigSources(mockhttp.ConfigArgs([]string{"--read-timeout", "1s"})))
		m.MustMock(mockhttp.Postf("/tea").Reply(mockhttp.Created()))
		start := time.Now()
		require.NoError(t, conn.SetReadDeadline(start.Add(2*time.Second)))
		_, err := io.WriteString(conn, "POST /tea HTTP/1.1\r\nHost: example.test\r\nContent-Length: 2\r\n\r\na")
		require.NoError(t, err)

		reader := bufio.NewReader(conn)
		res, err := http.ReadResponse(reader, nil)

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, time.Second, time.Since(start))
		_, err = reader.ReadByte()
		require.ErrorIs(t, err, io.EOF)
	})
}

func TestServer_InterceptedHTTPSReleasesIncompleteHeadersAtTheTimeout(t *testing.T) {
	for _, flag := range []string{"--read-header-timeout", "--read-timeout"} {
		t.Run(flag, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn, m := startPipeServer(t, mockhttp.Setup().
					ConfigSources(mockhttp.ConfigArgs([]string{flag, "1s"})).
					Proxy(&mockhttp.ProxyConfig{Intercept: true}))
				require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
				_, err := io.WriteString(conn, "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test:443\r\n\r\n")
				require.NoError(t, err)
				res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, res.StatusCode)

				roots := x509.NewCertPool()
				roots.AddCert(m.ProxyCA())
				client := tls.Client(conn, &tls.Config{
					RootCAs:    roots,
					ServerName: "example.test",
					NextProtos: []string{"http/1.1"},
				})
				require.NoError(t, client.HandshakeContext(t.Context()))
				start := time.Now()
				_, err = io.WriteString(client, "GET /tea HTTP/1.1\r\nHost: example.test\r\n")
				require.NoError(t, err)

				_, err = client.Read(make([]byte, 1))

				require.ErrorIs(t, err, io.EOF)
				require.Equal(t, time.Second, time.Since(start))
			})
		})
	}
}

func TestServer_HeaderTimeoutLetsTheBodyArriveLater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, m := startPipeServer(t, mockhttp.Setup().ConfigSources(mockhttp.ConfigArgs([]string{"--read-header-timeout", "1s"})))
		m.MustMock(mockhttp.Postf("/tea").Reply(mockhttp.Created()))
		require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
		_, err := io.WriteString(conn, "POST /tea HTTP/1.1\r\nHost: example.test\r\nContent-Length: 2\r\n\r\na")
		require.NoError(t, err)
		time.Sleep(2 * time.Second)
		_, err = io.WriteString(conn, "b")
		require.NoError(t, err)

		res, err := http.ReadResponse(bufio.NewReader(conn), nil)

		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusCreated, res.StatusCode)
	})
}

func TestServer_IdleKeepAliveReleasesTheConnectionAtTheTimeout(t *testing.T) {
	for _, flag := range []string{"--idle-timeout", "--read-timeout"} {
		t.Run(flag, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn, m := startPipeServer(t, mockhttp.Setup().ConfigSources(mockhttp.ConfigArgs([]string{flag, "1s"})))
				m.MustMock(mockhttp.Getf("/tea").Reply(mockhttp.Created()))
				require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
				_, err := io.WriteString(conn, "GET /tea HTTP/1.1\r\nHost: example.test\r\n\r\n")
				require.NoError(t, err)
				reader := bufio.NewReader(conn)
				res, err := http.ReadResponse(reader, nil)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusCreated, res.StatusCode)
				start := time.Now()

				_, err = reader.ReadByte()

				require.ErrorIs(t, err, io.EOF)
				require.Equal(t, time.Second, time.Since(start))
			})
		})
	}
}

func TestServer_InboundTimeoutsLetSlowResponseStreamsFinish(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "zero defaults"},
		{
			name: "configured inbound timeouts",
			args: []string{"--read-timeout", "1s", "--read-header-timeout", "1s", "--idle-timeout", "1s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				conn, m := startPipeServer(t, mockhttp.Setup().ConfigSources(mockhttp.ConfigArgs(tt.args)))
				m.MustMock(mockhttp.Getf("/tea").Reply(mockhttp.OK().EventStream(mockhttp.NewEventStream(
					mockhttp.NewEvent().Data("first"),
					mockhttp.NewEvent().Data("second").Delay(2*time.Second),
				))))
				start := time.Now()
				require.NoError(t, conn.SetReadDeadline(start.Add(4*time.Second)))
				_, err := io.WriteString(conn, "GET /tea HTTP/1.1\r\nHost: example.test\r\n\r\n")
				require.NoError(t, err)
				res, err := http.ReadResponse(bufio.NewReader(conn), nil)
				require.NoError(t, err)

				body, err := io.ReadAll(res.Body)

				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.Equal(t, "data: first\n\ndata: second\n\n", string(body))
				require.Equal(t, 2*time.Second, time.Since(start))
			})
		})
	}
}

func TestServer_ZeroTimeoutDefaultsPermitSlowRequestsAndIdleConnections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, m := startPipeServer(t, mockhttp.Setup().ConfigSources())
		m.MustMock(mockhttp.Postf("/tea").Reply(mockhttp.Created()))
		require.NoError(t, conn.SetDeadline(time.Now().Add(4*time.Hour)))
		_, err := io.WriteString(conn, "POST /tea HTTP/1.1\r\nHost: example.test\r\n")
		require.NoError(t, err)
		time.Sleep(time.Hour)
		_, err = io.WriteString(conn, "Content-Length: 2\r\n\r\na")
		require.NoError(t, err)
		time.Sleep(time.Hour)
		_, err = io.WriteString(conn, "b")
		require.NoError(t, err)
		reader := bufio.NewReader(conn)
		res, err := http.ReadResponse(reader, nil)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusCreated, res.StatusCode)
		time.Sleep(time.Hour)

		_, err = io.WriteString(conn, "POST /tea HTTP/1.1\r\nHost: example.test\r\nContent-Length: 0\r\n\r\n")
		require.NoError(t, err)
		res, err = http.ReadResponse(reader, nil)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusCreated, res.StatusCode)
	})
}

func startPipeServer(t *testing.T, setup *mockhttp.ConfigBuilder) (net.Conn, *mockhttp.Server) {
	t.Helper()

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = client.Close() })
	closed := make(chan struct{})
	m, err := mockhttp.New(setup.Silent().Listener(&pipeListener{
		conn:      server,
		closed:    closed,
		closeOnce: sync.OnceFunc(func() { close(closed) }),
	}))
	require.NoError(t, err)
	t.Cleanup(m.Close)
	require.NoError(t, m.Start())

	return client, m
}

type pipeListener struct {
	conn      net.Conn
	closed    chan struct{}
	closeOnce func()
	accepted  bool
}

func (l *pipeListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}

	<-l.closed
	return nil, net.ErrClosed
}

func (l *pipeListener) Close() error {
	l.closeOnce()
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.conn.LocalAddr() }
