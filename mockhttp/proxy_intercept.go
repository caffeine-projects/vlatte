package mockhttp

import (
	"bufio"
	"cmp"
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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/caffeine-projects/vlatte"
)

var _ net.Listener = (*oneConnListener)(nil)

const (
	// _handshakeTimeout bounds the start of an intercepted connection: the client's first bytes, and its TLS handshake.
	_handshakeTimeout = 10 * time.Second

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

	if !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
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

	return &interceptor{ca: cert, caKey: signer, key: key, leaves: make(map[string]*tls.Certificate)}, nil
}

// certificate returns the certificate of host, a name or an IP address, signed by the CA. It makes it once, and again
// once it is about to end.
func (i *interceptor) certificate(host string) (*tls.Certificate, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	now := time.Now()
	if leaf, ok := i.leaves[host]; ok && now.Before(leaf.Leaf.NotAfter.Add(-_leafRenewal)) {
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

	cert := &tls.Certificate{Certificate: [][]byte{der, i.ca.Raw}, PrivateKey: i.key, Leaf: leaf}
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
// proxy sends the rest on to the host. A client that doesn't start TLS goes to the host through a tunnel.
func (h *mockHandler) intercept(w http.ResponseWriter, r *RequestValues) {
	req := r.RawRequest

	client, err := hijack(w)
	if err != nil {
		h.onError(w, r, vlatte.FailureReply, fmt.Errorf("http: proxy: intercepting a CONNECT needs an HTTP/1.1 connection, "+
			"and a response writer that can hijack it, or that unwraps to one that can: %w", err))

		return
	}

	// A connection that switches protocols within is the switch's: the server tracks it, and closes it.
	owned := true
	defer func() {
		if owned {
			client.Close()
		}
	}()

	// The server is closing.
	if !h.app.tunnels.add(client) {
		return
	}

	defer h.app.tunnels.remove(client)

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: answering the CONNECT to %s: %w", req.Host, err))
		return
	}

	// The client speaks first. A TLS one starts with a handshake record, of type 22, within the time a handshake gets.
	_ = client.SetReadDeadline(time.Now().Add(_handshakeTimeout))

	in := bufio.NewReader(client)

	first, err := in.Peek(1)
	if err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: reading the intercepted connection to %s: %w", req.Host, err))
		return
	}

	conn := &bufferedConn{Conn: client, r: in}

	if first[0] != 0x16 {
		_ = client.SetReadDeadline(time.Time{})
		h.tunnelTo(conn, r)

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

	if err := tlsConn.Handshake(); err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: intercepting the TLS connection to %s: %w", req.Host, err))
		return
	}

	_ = client.SetReadDeadline(time.Time{})

	opened := newResponse()
	opened.StatusCode = http.StatusOK
	h.lifecycle.OnProxied(r, opened)

	owned = !h.serveIntercepted(tlsConn, req.Host)
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

	_ = srv.Serve(&oneConnListener{conn: conn})
	<-done

	return switched
}

// tunnelTo dials the host of r, a CONNECT, and pipes client, a connection already answered, to it.
func (h *mockHandler) tunnelTo(client net.Conn, r *RequestValues) {
	req := r.RawRequest
	via := append(slices.Clone(req.Header.Values(_viaHeader)), h.app.id)

	upstream, err := h.app.proxy.dial(req.Context(), req.Host, via)
	if err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: %w", err))
		return
	}

	defer upstream.Close()

	if !h.app.tunnels.add(upstream) {
		return
	}

	defer h.app.tunnels.remove(upstream)

	opened := newResponse()
	opened.StatusCode = http.StatusOK
	h.lifecycle.OnProxied(r, opened)

	if err := pipe(client, upstream); err != nil {
		h.lifecycle.OnWarning(r, fmt.Errorf("http: proxy: tunnel to %s: %w", req.Host, err))
	}
}

// oneConnListener is the listener of one connection, accepted already: its first Accept returns it, and the next
// ones fail, so the server that serves it doesn't wait for more.
type oneConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })

	if conn == nil {
		return nil, net.ErrClosed
	}

	return conn, nil
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
