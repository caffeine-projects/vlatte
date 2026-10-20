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
	"strings"
	"sync"
	"time"
)

var _ net.Conn = (*bufferedConn)(nil)

// tunnels holds the connections of the open tunnels. The server doesn't track a connection once it is hijacked, so
// Server.Close closes them.
type tunnels struct {
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

// add tracks conns until remove. It reports false, and tracks nothing, once closeAll ran: the caller closes them.
func (t *tunnels) add(conns ...net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return false
	}

	if t.conns == nil {
		t.conns = make(map[net.Conn]struct{})
	}

	for _, c := range conns {
		t.conns[c] = struct{}{}
	}

	return true
}

// remove stops tracking conns.
func (t *tunnels) remove(conns ...net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, c := range conns {
		delete(t.conns, c)
	}
}

// closeAll closes the connections it tracks, and those added later.
func (t *tunnels) closeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.closed = true

	for c := range t.conns {
		_ = c.Close()
	}

	clear(t.conns)
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

// CloseWrite ends what goes out on the connection, so the peer reads to its end, and leaves what comes in open.
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
func pipe(client, upstream net.Conn) error {
	errs := make(chan error, 2)
	copyTo := func(dst, src net.Conn) {
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
func closeWrite(c net.Conn) error {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}

	return c.Close()
}

// dial opens a connection to host, the host:port of a CONNECT, for a tunnel, within the proxy's timeout. It goes
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

	var dialer net.Dialer

	if proxyURL == nil {
		conn, err := dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			return nil, fmt.Errorf("proxy: opening a tunnel to %s: %w", host, err)
		}

		return conn, nil
	}

	conn, err := p.dialVia(ctx, &dialer, proxyURL, host, via)
	if err != nil {
		return nil, fmt.Errorf("proxy: opening a tunnel to %s through %s: %w", host, displayURL(proxyURL), err)
	}

	return conn, nil
}

// dialVia opens a connection to host through the proxy at proxyURL, an http or https URL, with a CONNECT.
func (p *forwardProxy) dialVia(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, host string, via []string) (net.Conn, error) {
	port := proxyURL.Port()
	switch {
	case port != "":
	case proxyURL.Scheme == "https":
		port = "443"
	default:
		port = "80"
	}

	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), port))
	if err != nil {
		return nil, err
	}

	// The context bounds the handshakes too: once it ends, they fail.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()

	if proxyURL.Scheme == "https" {
		cfg := p.owned.TLSClientConfig.Clone()
		cfg.ServerName = proxyURL.Hostname()

		tlsConn := tls.Client(conn, cfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
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
	if !stop() || conn.SetDeadline(time.Time{}) != nil {
		_ = conn.Close()
		return nil, context.Cause(ctx)
	}

	return &bufferedConn{Conn: conn, r: br}, nil
}

// cameBack reports whether h, the header of a request, holds id in its VLatte-Via: the request went through the server
// with that ID before, and came back.
func cameBack(h http.Header, id string) bool {
	for _, v := range h.Values(_viaHeader) {
		for seen := range strings.SplitSeq(v, ",") {
			if strings.TrimSpace(seen) == id {
				return true
			}
		}
	}

	return false
}
