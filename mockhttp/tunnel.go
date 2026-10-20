package mockhttp

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	netproxy "golang.org/x/net/proxy"
)

var _ net.Conn = (*bufferedConn)(nil)

// tunnels holds the connections of the open tunnels, CONNECT ones and those of protocol switches. The server doesn't
// track a connection once it is hijacked, nor wait for its handler, so Server.Close closes them, and waits for each add
// to be removed.
type tunnels struct {
	mu     sync.Mutex
	conns  map[io.Closer]struct{}
	closed bool
	open   sync.WaitGroup // an add that wasn't removed yet
}

// add tracks conns until a remove of them. It reports false, and tracks nothing, once closeAll ran: the caller closes
// them.
func (t *tunnels) add(conns ...io.Closer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return false
	}

	t.open.Add(1)

	if t.conns == nil {
		t.conns = make(map[io.Closer]struct{})
	}

	for _, c := range conns {
		t.conns[c] = struct{}{}
	}

	return true
}

// remove stops tracking conns, which one add tracked.
func (t *tunnels) remove(conns ...io.Closer) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, c := range conns {
		delete(t.conns, c)
	}

	t.open.Done()
}

// closeAll closes the connections it tracks, and those added later, and waits until the handlers that added them
// removed them.
func (t *tunnels) closeAll() {
	t.mu.Lock()

	t.closed = true

	for c := range t.conns {
		_ = c.Close()
	}

	clear(t.conns)
	t.mu.Unlock()

	t.open.Wait()
}

// bufferedConn is a connection whose first bytes were read into r already, such as by an HTTP parser: it reads them
// first.
type bufferedConn struct {
	net.Conn

	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// CloseWrite ends what goes out on the connection, so the peer reads to its end, and leaves what comes in open. A
// connection that can't is closed.
func (c *bufferedConn) CloseWrite() error {
	return closeWrite(c.Conn)
}

// hijack takes the client's connection over from w, for a tunnel. The bytes the server read past the request, which
// the client sent ahead, come first. It fails with http.ErrNotSupported when no writer w unwraps to can hijack, such
// as over HTTP/2, or behind a writer of a middleware that doesn't unwrap.
func hijack(w http.ResponseWriter) (net.Conn, error) {
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, err
	}

	return &bufferedConn{Conn: conn, r: rw.Reader}, nil
}

// pipe copies client to upstream and upstream to client until both directions end. A side that ends what it sends has
// the other side read to an end, so the other direction goes on until its own end. A side that breaks off ends both:
// the other direction has nowhere to go. It returns the first error that isn't the end of a connection.
func pipe(client, upstream io.ReadWriteCloser) error {
	errs := make(chan error, 2)
	copyTo := func(dst, src io.ReadWriteCloser) {
		_, err := io.Copy(dst, src)
		if err != nil {
			_ = dst.Close()
			_ = src.Close()
		} else {
			_ = closeWrite(dst)
		}

		errs <- err
	}

	go copyTo(upstream, client)
	go copyTo(client, upstream)

	var err error
	for range 2 {
		if e := <-errs; err == nil && e != nil && !errors.Is(e, net.ErrClosed) {
			err = e
		}
	}

	return err
}

// closeWrite ends what goes out on c, and leaves what comes in open, when c can: a connection that can't is closed.
func closeWrite(c io.ReadWriteCloser) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		if err := cw.CloseWrite(); err == nil {
			return nil
		}
	}

	return c.Close()
}

// dial opens a connection to host, the host:port of a CONNECT, for a tunnel, within the proxy's timeout, and within the
// limits of the transport the proxy makes: to connect, and for a TLS handshake with an https proxy on the way. It goes
// through the proxy that the transport the proxy made picks for host, Via or the proxy of the environment, with a
// CONNECT of its own, which carries via, the VLatte-Via of the request. Without one, or with a transport of the
// caller's, it dials host.
func (p *forwardProxy) dial(ctx context.Context, host string, via []string) (net.Conn, error) {
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)

		defer cancel()
	}

	var proxyURL *url.URL
	if p.owned != nil && p.owned.Proxy != nil {
		var err error
		if proxyURL, err = p.owned.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: host}}); err != nil {
			return nil, fmt.Errorf("proxy: picking the proxy of %s: %w", host, err)
		}
	}

	if proxyURL == nil {
		conn, err := p.dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			return nil, fmt.Errorf("proxy: opening a tunnel to %s: %w", host, err)
		}

		return conn, nil
	}

	conn, err := p.dialVia(ctx, proxyURL, host, via)
	if err != nil {
		return nil, fmt.Errorf("proxy: opening a tunnel to %s through %s: %w", host, displayURL(proxyURL), err)
	}

	return conn, nil
}

// dialVia opens a connection to host through the proxy at proxyURL: a SOCKS5 one, or an http or https one, with a
// CONNECT.
func (p *forwardProxy) dialVia(ctx context.Context, proxyURL *url.URL, host string, via []string) (net.Conn, error) {
	addr := viaAddr(proxyURL)

	if proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h" {
		return dialSOCKS5(ctx, p.dialer, addr, proxyURL.User, host)
	}

	conn, err := p.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	// The context bounds the handshakes too: once it ends, they fail.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	if proxyURL.Scheme == "https" {
		cfg := p.owned.TLSClientConfig.Clone()
		cfg.ServerName = proxyURL.Hostname()

		handshake, cancel := context.WithTimeout(ctx, p.handshakeTimeout)
		tlsConn := tls.Client(conn, cfg)
		err := tlsConn.HandshakeContext(handshake)

		cancel()

		if err != nil {
			_ = conn.Close()
			return nil, err
		}

		conn = tlsConn
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: host},
		Host:   host,
		Header: http.Header{_viaHeader: via},
	}

	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}

	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// The answer to a CONNECT has no body: what br reads after it is the tunnel's.
	br := bufio.NewReader(conn)

	res, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("the proxy answered the CONNECT with %s", res.Status)
	}

	// The deadline of the handshakes doesn't bound the tunnel.
	if !stop() {
		_ = conn.Close()
		return nil, context.Cause(ctx)
	}

	// Clearing a deadline fails only on a closed connection, and nothing closes conn before it is returned.
	_ = conn.SetDeadline(time.Time{})

	return &bufferedConn{Conn: conn, r: br}, nil
}

// viaAddr returns the host:port of proxyURL, a Via, with the default port of its scheme when it names none.
func viaAddr(proxyURL *url.URL) string {
	port := proxyURL.Port()
	switch {
	case port != "":
	case proxyURL.Scheme == "https":
		port = "443"
	case proxyURL.Scheme == "http":
		port = "80"
	default:
		port = "1080"
	}

	return net.JoinHostPort(proxyURL.Hostname(), port)
}

// dialSOCKS5 opens a connection to host through the SOCKS5 proxy at addr, with the credentials of user, if any.
func dialSOCKS5(ctx context.Context, dialer *net.Dialer, addr string, user *url.Userinfo, host string) (net.Conn, error) {
	var auth *netproxy.Auth
	if user != nil {
		password, _ := user.Password()
		auth = &netproxy.Auth{User: user.Username(), Password: password}
	}

	// The connection the SOCKS5 one wraps can end what it sends, which the SOCKS5 one can't: a tunnel needs it to.
	forward := &keepingDialer{dialer: dialer}

	d, err := netproxy.SOCKS5("tcp", addr, auth, forward)
	if err != nil {
		return nil, err
	}

	cd, ok := d.(netproxy.ContextDialer)
	if !ok {
		return nil, errors.New("the SOCKS5 dialer doesn't take a context")
	}

	conn, err := cd.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}

	return &halfClosingConn{Conn: conn, raw: forward.conn}, nil
}

// keepingDialer dials with dialer, and keeps the connection it dialed last.
type keepingDialer struct {
	dialer *net.Dialer
	conn   net.Conn
}

func (d *keepingDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

func (d *keepingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, network, addr)
	d.conn = conn

	return conn, err
}

// halfClosingConn is a connection over raw, such as a SOCKS5 one, which ends what it sends by ending raw's.
type halfClosingConn struct {
	net.Conn

	raw net.Conn
}

// CloseWrite ends what goes out on raw, and leaves what comes in open.
func (c *halfClosingConn) CloseWrite() error {
	return closeWrite(c.raw)
}
