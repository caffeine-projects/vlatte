package mockhttp

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// signedCA returns a CA signed by parent, or self-signed when parent is nil, with usage as its key usages: 0 leaves
// them out, as openssl does by default.
func signedCA(t *testing.T, name string, usage x509.KeyUsage, parent *tls.Certificate) *tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              usage,
	}

	issuer, issuerKey := template, any(key)
	if parent != nil {
		issuer, issuerKey = parent.Leaf, parent.PrivateKey
	}

	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	chain := [][]byte{der}
	if parent != nil {
		chain = append(chain, parent.Certificate...)
	}

	return &tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: cert}
}

func TestInterceptor_TakesACAWithoutKeyUsages(t *testing.T) {
	// openssl makes a CA without key usages by default, which x509 lets sign: such a CA was refused.
	in, err := newInterceptor(signedCA(t, "openssl CA", 0, nil))
	require.NoError(t, err)

	_, err = in.certificate("api.local")
	require.NoError(t, err)
}

func TestInterceptor_SendsTheChainOfAnIntermediateCA(t *testing.T) {
	// An intermediate CA comes with its chain, which clients that trust only the root need: it was left out.
	root := signedCA(t, "root", x509.KeyUsageCertSign, nil)
	intermediate := signedCA(t, "intermediate", x509.KeyUsageCertSign, root)

	in, err := newInterceptor(intermediate)
	require.NoError(t, err)

	cert, err := in.certificate("api.local")
	require.NoError(t, err)

	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root.Leaf)

	for _, der := range cert.Certificate[1:] {
		c, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		intermediates.AddCert(c)
	}

	_, err = cert.Leaf.Verify(x509.VerifyOptions{DNSName: "api.local", Roots: roots, Intermediates: intermediates})
	require.NoError(t, err)
}

func TestInterceptor_RefusesACAItCantSignWith(t *testing.T) {
	// A CA that can't sign the hosts' certificates fails New, saying why, rather than every handshake later. One given
	// without its parsed Leaf, as tls.Certificate may come, is parsed.
	ca := signedCA(t, "caller CA", x509.KeyUsageCertSign, nil)

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tests := []struct {
		name string
		ca   *tls.Certificate
		want string
	}{
		{name: "no certificate", ca: &tls.Certificate{}, want: "proxy: the CA has no certificate"},
		{
			name: "a certificate that doesn't parse",
			ca:   &tls.Certificate{Certificate: [][]byte{[]byte("not DER")}, PrivateKey: ca.PrivateKey},
			want: "proxy: the CA's certificate: ",
		},
		{
			name: "a key that can't sign",
			ca:   &tls.Certificate{Certificate: ca.Certificate, PrivateKey: "not a key", Leaf: ca.Leaf},
			want: "proxy: the CA's private key can't sign",
		},
		{
			name: "another certificate's key",
			ca:   &tls.Certificate{Certificate: ca.Certificate, PrivateKey: other, Leaf: ca.Leaf},
			want: `proxy: the CA's private key isn't the key of its certificate, of "caller CA"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, CA: tt.ca}))
			require.ErrorContains(t, err, tt.want)
		})
	}

	t.Run("no Leaf", func(t *testing.T) {
		m, err := New(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, CA: &tls.Certificate{Certificate: ca.Certificate, PrivateKey: ca.PrivateKey}}))
		require.NoError(t, err)
		t.Cleanup(m.Close)
		require.True(t, ca.Leaf.Equal(m.ProxyCA()))
	})
}

func TestInterceptor_KeepsTheCertificateOfEachHost(t *testing.T) {
	// A host's certificate is made once, and kept for the next connections: making one is the slow part of a
	// handshake. Each host has its own.
	in, err := newInterceptor(signedCA(t, "CA", x509.KeyUsageCertSign, nil))
	require.NoError(t, err)

	first, err := in.certificate("a.local")
	require.NoError(t, err)

	again, err := in.certificate("a.local")
	require.NoError(t, err)
	require.Same(t, first, again)

	other, err := in.certificate("b.local")
	require.NoError(t, err)
	require.NotEqual(t, first.Leaf.SerialNumber, other.Leaf.SerialNumber)
	require.Equal(t, []string{"b.local"}, other.Leaf.DNSNames)
}

func TestInterceptor_KeepsABoundedNumberOfCertificates(t *testing.T) {
	// A client can ask for any name: the certificates kept are bounded, or a long run of names would grow them without
	// end. Past the bound, the interceptor starts over.
	in, err := newInterceptor(signedCA(t, "CA", x509.KeyUsageCertSign, nil))
	require.NoError(t, err)

	for i := range _maxLeaves + 1 {
		_, err := in.certificate(fmt.Sprintf("h%d.local", i))
		require.NoError(t, err)
		require.LessOrEqual(t, len(in.leaves), _maxLeaves)
	}

	require.Len(t, in.leaves, 1)
}

// interceptLogs starts a forward proxy that intercepts, with its log, and the middlewares of use.
func interceptLogs(t *testing.T, use ...func(http.Handler) http.Handler) (*Server, *lockedBuffer) {
	t.Helper()

	logs := &lockedBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false).Proxy(&ProxyConfig{Intercept: true}).Use(use...))
	m.MustStart()
	t.Cleanup(m.Close)

	return m, logs
}

// requireWarned fails t unless logs come to warn about want.
func requireWarned(t *testing.T, logs *lockedBuffer, want string) {
	t.Helper()

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), `"level":"warn"`) && strings.Contains(logs.String(), want)
	}, 2*time.Second, 10*time.Millisecond, "the log didn't warn about %q", want)
}

// acceptedHost starts a TCP host that closes each connection, and reports on dialed that it was dialed.
func acceptedHost(t *testing.T) (string, <-chan struct{}) {
	t.Helper()

	dialed := make(chan struct{}, 10)

	return tcpHost(t, func(net.Conn) { dialed <- struct{}{} }), dialed
}

func TestForwardProxy_AnInterceptedTLSStartThatFailsIsClosed(t *testing.T) {
	// A connection that starts a TLS record but whose ClientHello can't be read, or isn't one, is no TLS client to
	// serve, nor another protocol to tunnel: it is closed, and the log warns, without the host being dialed.
	header := func(length int) []byte { return []byte{0x16, 0x03, 0x01, byte(length >> 8), byte(length)} }

	tests := map[string][]byte{
		"a header cut short":              {0x16, 0x03, 0x01},
		"a record cut short":              append(header(100), make([]byte, 10)...),
		"a record over the size limit":    header(0xFFFF),
		"a record that is no ClientHello": append(header(8), 0x02, 0x00, 0x00, 0x04, 0x03, 0x03, 0x00, 0x00),
		"a record of no TLS version":      append([]byte{0x16, 0x16, 0x03, 0x30, 0x00}, make([]byte, 0x3000)...),
	}

	for name, first := range tests {
		t.Run(name, func(t *testing.T) {
			host, dialed := acceptedHost(t)
			m, logs := interceptLogs(t)

			conn, _ := openTunnel(t, hostOf(t, m), host, "")

			_, err := conn.Write(first)
			require.NoError(t, err)
			require.NoError(t, conn.(*net.TCPConn).CloseWrite())

			_, err = io.ReadAll(conn)
			require.NoError(t, err, "the connection stayed open")

			requireWarned(t, logs, "http: proxy: intercepting the TLS connection to "+host)

			select {
			case <-dialed:
				t.Fatal("the host was dialed")
			default:
			}
		})
	}
}

func TestForwardProxy_AnInterceptedClientThatLeavesIsAWarning(t *testing.T) {
	// A client that leaves once its CONNECT is answered, before it sends anything, is logged as gone, and its host
	// isn't dialed.
	host, dialed := acceptedHost(t)
	m, logs := interceptLogs(t)

	conn, _ := openTunnel(t, hostOf(t, m), host, "")
	require.NoError(t, conn.Close())

	requireWarned(t, logs, "http: proxy: reading the intercepted connection to "+host)

	select {
	case <-dialed:
		t.Fatal("the host was dialed")
	default:
	}
}

func TestForwardProxy_AnInterceptedTunnelToAHostThatIsGoneIsClosed(t *testing.T) {
	// A connection that goes to its host through a tunnel, as one that doesn't start TLS or offers only h2 does, is
	// closed when the host can't be reached: its 200 went out already. The log says why.
	host := closedAddr(t)

	for name, start := range map[string]func(conn net.Conn){
		"a client that doesn't start TLS": func(conn net.Conn) { _, _ = io.WriteString(conn, "plain\n") },
		"a client that offers only h2": func(conn net.Conn) {
			_ = tls.Client(conn, &tls.Config{ServerName: "127.0.0.1", NextProtos: []string{"h2"}, InsecureSkipVerify: true}).Handshake()
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, logs := interceptLogs(t)

			conn, _ := openTunnel(t, hostOf(t, m), host, "")
			start(conn)

			_, _ = io.ReadAll(conn)

			requireWarned(t, logs, "proxy: opening a tunnel to "+host)
		})
	}
}

func TestForwardProxy_InterceptsAConnectWithoutAPort(t *testing.T) {
	// A CONNECT may name its host without a port. A client that sends no name, as for an IP address, gets a
	// certificate for the host the CONNECT names.
	m := MustNew(Setup().Silent().Proxy(&ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/mocked").Reply(OK().PlainText("mock")))

	conn, _ := openTunnel(t, hostOf(t, m), "127.0.0.1", "")

	roots := x509.NewCertPool()
	roots.AddCert(m.ProxyCA())

	client := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1"})
	require.NoError(t, client.Handshake())
	require.Equal(t, []net.IP{net.ParseIP("127.0.0.1").To4()}, client.ConnectionState().PeerCertificates[0].IPAddresses)

	_, err := io.WriteString(client, "GET /mocked HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)

	res, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "mock", string(body))
}

// readOnceConn is a connection whose reads fail with errBroken after the first one.
type readOnceConn struct {
	net.Conn

	read atomic.Bool
}

func (c *readOnceConn) Read(p []byte) (int, error) {
	if c.read.Swap(true) {
		return 0, errBroken
	}

	return c.Conn.Read(p)
}

// readOnceWriter is the writer of a middleware, which hands its connection over as a readOnceConn.
type readOnceWriter struct {
	http.ResponseWriter
}

func (w readOnceWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, _, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}

	c := &readOnceConn{Conn: conn}

	return c, bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c)), nil
}

func TestForwardProxy_AnInterceptedConnectionThatBreaksIsAWarning(t *testing.T) {
	// A client's connection that breaks once taken over can't get the server's answer to a failure: the log warns,
	// saying why, whether it breaks before the 200, before its first byte, or within the tunnel to its host.
	tests := []struct {
		name string
		use  func(http.Handler) http.Handler
		send string
		want string
	}{
		{name: "before the 200", use: breaking(false, true), want: "http: proxy: answering the CONNECT to "},
		{name: "before its first byte", use: breaking(true, false), want: "http: proxy: reading the intercepted connection to "},
		{
			name: "within the tunnel",
			use: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(readOnceWriter{w}, r) })
			},
			send: "plain\n",
			want: "http: proxy: tunnel to ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := tcpHost(t, echoConn)
			m, logs := interceptLogs(t, tt.use)

			conn, err := net.Dial("tcp", hostOf(t, m))
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))

			_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
			require.NoError(t, err)

			br := bufio.NewReader(conn)
			if res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect}); err == nil && tt.send != "" {
				require.Equal(t, http.StatusOK, res.StatusCode)

				_, err = io.WriteString(conn, tt.send)
				require.NoError(t, err)
			}

			_, _ = io.ReadAll(br)

			requireWarned(t, logs, tt.want+host)
		})
	}
}

func TestForwardProxy_InterceptionNeedsAConnectionItCanTakeOver(t *testing.T) {
	// Behind a writer that can't hand its connection over, the client gets the server's answer to a failure, saying
	// why. While the server closes, the connection is closed without an answer.
	host := tcpHost(t, echoConn)

	t.Run("a writer that can't", func(t *testing.T) {
		m, _ := interceptLogs(t, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(opaqueWriter{w}, r) })
		})

		res, body := connect(t, m, host)
		require.Equal(t, m.Config().ErrorStatusCode, res.StatusCode)
		require.Contains(t, body, "a tunnel needs an HTTP/1.1 connection, and a response writer that can hijack it")
	})

	t.Run("a server that closes", func(t *testing.T) {
		m, _ := interceptLogs(t)
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
	})
}

func TestForwardProxy_AnInterceptedClientThatStallsIsClosed(t *testing.T) {
	// The first byte of an intercepted connection had a limit, and the handshake one, but not what came between: a
	// client that started a TLS record and held back the rest held its connection until it sent more, left, or the
	// server closed. The handshake's limit bounds the whole ClientHello, and one that doesn't come in time fails the
	// handshake, which closes the connection.
	host, dialed := acceptedHost(t)

	logs := &lockedBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).LogPretty(false).Proxy(&ProxyConfig{Intercept: true}))
	m.proxy.handshakeTimeout = 200 * time.Millisecond
	m.MustStart()
	t.Cleanup(m.Close)

	conn, _ := openTunnel(t, hostOf(t, m), host, "")

	// A record header for 100 bytes, and 10 of them: the rest never comes.
	_, err := conn.Write(append([]byte{0x16, 0x03, 0x01, 0x00, 100}, make([]byte, 10)...))
	require.NoError(t, err)

	_, err = io.ReadAll(conn)
	require.NoError(t, err, "the connection stayed open")

	requireWarned(t, logs, "http: proxy: intercepting the TLS connection to "+host)

	select {
	case <-dialed:
		t.Fatal("the host was dialed")
	default:
	}
}

func TestOneConnListener_HandsItsConnectionOverOnce(t *testing.T) {
	// The server of an intercepted connection takes it, then waits on Accept until the connection is over, and
	// returns: it returns neither before, nor ever waits for another connection.
	conn, peer := net.Pipe()
	t.Cleanup(func() {
		_ = conn.Close()
		_ = peer.Close()
	})

	l := &oneConnListener{conn: conn, done: make(chan struct{})}

	got, err := l.Accept()
	require.NoError(t, err)
	require.Same(t, conn, got)
	require.Equal(t, conn.LocalAddr(), l.Addr())
	require.NoError(t, l.Close())

	next := make(chan error, 1)

	go func() {
		_, err := l.Accept()
		next <- err
	}()

	select {
	case err := <-next:
		t.Fatalf("Accept returned before the connection was over: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(l.done)
	require.ErrorIs(t, <-next, net.ErrClosed)
}

func TestForwardProxy_AHostNameACertificateCantHoldFailsTheHandshake(t *testing.T) {
	// crypto/tls passes on a server name that isn't ASCII, which no certificate can hold: the handshake fails, and the
	// log says why.
	host, dialed := acceptedHost(t)
	m, logs := interceptLogs(t)

	conn, _ := openTunnel(t, hostOf(t, m), host, "")

	client := tls.Client(conn, &tls.Config{ServerName: "bücher.example", InsecureSkipVerify: true})
	require.Error(t, client.Handshake())

	requireWarned(t, logs, "proxy: making the certificate of bücher.example: ")

	select {
	case <-dialed:
		t.Fatal("the host was dialed")
	default:
	}
}
