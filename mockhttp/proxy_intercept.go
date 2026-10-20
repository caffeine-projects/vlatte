package mockhttp

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	_ net.Listener = (*oneConnListener)(nil)
	_ net.Conn     = (*recordConn)(nil)
)

// errHelloRead stops the handshake that reads a ClientHello for peekALPN.
var errHelloRead = errors.New("proxy: the ClientHello was read")

const (
	// _firstBytesWait is how long an intercepted connection waits for the client to start TLS. A client that sends
	// nothing in that time waits for its host to speak first, as an SSH client does: it goes to the host.
	_firstBytesWait = 2 * time.Second

	// _handshakeTimeout bounds the TLS handshake of an intercepted connection.
	_handshakeTimeout = 10 * time.Second

	// _maxLeaves bounds how many certificates of hosts an interceptor keeps: a client can ask for any name.
	_maxLeaves = 1024

	// _maxRecord is the size of a TLS record at most, its header included, so a ClientHello's fits in a peek.
	_maxRecord = 5 + 1<<14 + 2048

	// _caValidity is how long a CA the server makes for interception is valid.
	_caValidity = 365 * 24 * time.Hour

	// _leafValidity is how long the certificate of an intercepted host is valid, at most: the CA's end comes first.
	_leafValidity = 30 * 24 * time.Hour

	// _leafRenewal renews a certificate of an intercepted host this long before it ends.
	_leafRenewal = time.Hour
)

// interceptor makes the certificates of the hosts the forward proxy intercepts, signed by its CA, which clients trust.
type interceptor struct {
	ca     *x509.Certificate
	chain  [][]byte // the CA's certificates, which follow each host's: an intermediate CA's chain too
	caKey  crypto.Signer
	key    *ecdsa.PrivateKey // of every host's certificate: making one per host would slow the handshakes
	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// newInterceptor returns an interceptor that signs with ca, a CA's certificate and key.
func newInterceptor(ca *tls.Certificate) (*interceptor, error) {
	if ca == nil || len(ca.Certificate) == 0 {
		return nil, errors.New("proxy: the CA has no certificate")
	}

	cert := ca.Leaf
	if cert == nil {
		var err error
		if cert, err = x509.ParseCertificate(ca.Certificate[0]); err != nil {
			return nil, fmt.Errorf("proxy: the CA's certificate: %w", err)
		}
	}

	// A certificate without key usages, as openssl makes, may sign: x509 lets it.
	if !cert.IsCA || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0) {
		return nil, fmt.Errorf("proxy: the certificate of %q is no CA's: it can't sign certificates", cert.Subject.CommonName)
	}

	signer, ok := ca.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("proxy: the CA's private key can't sign")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("proxy: making the key of the hosts' certificates: %w", err)
	}

	return &interceptor{ca: cert, chain: ca.Certificate, caKey: signer, key: key, leaves: make(map[string]*tls.Certificate)}, nil
}

// certificate returns the certificate of host, a name or an IP address, signed by the CA. It makes it once, and again
// once it is about to end.
func (i *interceptor) certificate(host string) (*tls.Certificate, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// A certificate is renewed before it ends, unless it ends with the CA, which no renewal outlives.
	now := time.Now()
	if leaf, ok := i.leaves[host]; ok && now.Before(leaf.Leaf.NotAfter) &&
		(now.Before(leaf.Leaf.NotAfter.Add(-_leafRenewal)) || leaf.Leaf.NotAfter.Equal(i.ca.NotAfter)) {
		return leaf, nil
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	notAfter := now.Add(_leafValidity)
	if i.ca.NotAfter.Before(notAfter) {
		notAfter = i.ca.NotAfter
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, i.ca, &i.key.PublicKey, i.caKey)
	if err != nil {
		return nil, fmt.Errorf("proxy: making the certificate of %s: %w", host, err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	cert := &tls.Certificate{Certificate: append([][]byte{der}, i.chain...), PrivateKey: i.key, Leaf: leaf}

	if len(i.leaves) >= _maxLeaves {
		clear(i.leaves)
	}

	i.leaves[host] = cert

	return cert, nil
}

// newCA returns a CA of its own for a forward proxy that intercepts, valid for _caValidity.
func newCA() (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "vlatte proxy CA", Organization: []string{"vlatte"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(_caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}, nil
}

// intercept answers a CONNECT no mock matched on the client's connection, which it takes over. A client that starts
// TLS gets a certificate for the host the CONNECT names, signed by the proxy's CA, and its requests are served by the
// server, as requests in absolute form for that host: mocks answer them, recordings record them, and the forward
// proxy sends the rest on to the host. A client that doesn't start TLS goes to the host through a tunnel, as does one
// whose host speaks first, such as an SSH server: it sends nothing for _firstBytesWait.
func (h *mockHandler) intercept(w http.ResponseWriter, r *RequestValues) {
	req := r.RawRequest

	client, ok := h.openConnect(w, r)
	if !ok {
		return
	}

	// Once the connection is taken over, a failed read on it, such as when the client doesn't speak first, ends the
	// request's context: what follows has a context of its own, with the request's values, which Close ends.
	ctx, cancel := h.app.tunnels.bind(context.WithoutCancel(req.Context()))
	defer cancel()

	// A connection that switches protocols within is the switch's: it closes it.
	switched := false
	defer func() {
		if switched {
			h.app.tunnels.remove(client)
		} else {
			h.closeConnect(client)
		}
	}()

	// A TLS client speaks first, with a handshake record, of type 22.
	_ = client.SetReadDeadline(time.Now().Add(_firstBytesWait))

	in := bufio.NewReaderSize(client, _maxRecord)
	first, err := in.Peek(1)

	_ = client.SetReadDeadline(time.Time{})

	conn := &bufferedConn{Conn: client, r: in}

	switch {
	case errors.Is(err, os.ErrDeadlineExceeded) || (err == nil && first[0] != 0x16):
		upstream, err := h.dialHost(ctx, req)
		if err != nil {
			h.lifecycle.OnWarning(r, fmt.Errorf("http: %w", err))
			return
		}

		h.tunnel(conn, upstream, r)

		return
	case err != nil:
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: reading the intercepted connection to %s: %w", req.Host, err))
		return
	}

	// A client that can't speak HTTP/1.1, such as a gRPC one, which offers only h2, goes to the host through a tunnel:
	// the interception serves HTTP/1.1.
	if protocols, err := peekALPN(in); err == nil && len(protocols) > 0 && !slices.Contains(protocols, "http/1.1") {
		upstream, err := h.dialHost(ctx, req)
		if err != nil {
			h.lifecycle.OnWarning(r, fmt.Errorf("http: %w", err))
			return
		}

		h.tunnel(conn, upstream, r)

		return
	}

	host, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		host = req.Host
	}

	tlsConn := tls.Server(conn, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return h.app.proxy.interceptor.certificate(cmp.Or(hello.ServerName, host))
		},
		NextProtos: []string{"http/1.1"},
	})

	handshake, cancel := context.WithTimeout(ctx, _handshakeTimeout)
	err = tlsConn.HandshakeContext(handshake)

	cancel()

	if err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: intercepting the TLS connection to %s: %w", req.Host, err))
		return
	}

	h.logConnect(r)

	switched = h.serveIntercepted(tlsConn, req.Host)
}

// serveIntercepted serves the requests of conn, an intercepted TLS connection to authority, with the server's
// handler, as requests in absolute form for https://authority, until the connection ends, or switches protocols. It
// reports whether it switched: the switch has the connection then.
func (h *mockHandler) serveIntercepted(conn *tls.Conn, authority string) bool {
	var (
		once     sync.Once
		switched bool
	)

	done := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme, r.URL.Host = "https", authority
			if strings.HasPrefix(r.RequestURI, "/") {
				r.RequestURI = "https://" + authority + r.RequestURI
			}

			h.app.handler.ServeHTTP(w, r)
		}),
		ErrorLog: log.New(h.app.logger, "proxy: intercepted connection: ", 0),
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				once.Do(func() {
					switched = state == http.StateHijacked
					close(done)
				})
			}
		},
	}

	l := &oneConnListener{conn: conn}
	_ = srv.Serve(l)

	// A server that failed before it took the connection never serves it.
	if !l.accepted.Load() {
		return false
	}

	<-done

	return switched
}

// peekALPN returns the protocols the TLS ClientHello that in starts with offers by ALPN, and leaves the ClientHello to
// be read. crypto/tls reads it, from a copy of the record that holds it, up to where a server picks its config.
func peekALPN(in *bufio.Reader) ([]string, error) {
	header, err := in.Peek(5)
	if err != nil {
		return nil, err
	}

	record, err := in.Peek(5 + (int(header[3])<<8 | int(header[4])))
	if err != nil {
		return nil, err
	}

	var (
		protocols []string
		read      bool
	)

	_ = tls.Server(&recordConn{r: bytes.NewReader(record)}, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			protocols, read = slices.Clone(hello.SupportedProtos), true
			return nil, errHelloRead
		},
	}).Handshake()

	if !read {
		return nil, errors.New("proxy: no TLS ClientHello in the first record")
	}

	return protocols, nil
}

// recordConn is a connection that reads r, such as a copy of a TLS record, and drops what is written to it.
type recordConn struct {
	r io.Reader
}

func (c *recordConn) Read(p []byte) (int, error)       { return c.r.Read(p) }
func (c *recordConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *recordConn) Close() error                     { return nil }
func (c *recordConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *recordConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *recordConn) SetDeadline(time.Time) error      { return nil }
func (c *recordConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordConn) SetWriteDeadline(time.Time) error { return nil }

// oneConnListener is the listener of one connection, accepted already: its first Accept returns it, and the next
// ones fail, so the server that serves it doesn't wait for more.
type oneConnListener struct {
	conn     net.Conn
	accepted atomic.Bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.accepted.Swap(true) {
		return nil, net.ErrClosed
	}

	return l.conn, nil
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
