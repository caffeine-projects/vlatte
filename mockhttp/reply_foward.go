package mockhttp

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ ReplierFactory = (*ProxyReply)(nil)
	_ Replier        = (*proxyReplier)(nil)
	_ io.Closer      = (*proxyReplier)(nil)
	_ io.ReadCloser  = (*targetBody)(nil)
	_ keptBody       = (*targetBody)(nil)
)

// errNoHeaders ends a call to a target that didn't send its response headers within the proxy's timeout.
var errNoHeaders = errors.New("reply_proxy: no response headers in time")

var _forbiddenHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

const (
	_defaultTimeout = 30 * time.Second

	// _keptBodyLimit bounds how much of a target's body a proxy keeps, for the request log, callbacks and events,
	// while it streams to the client: a larger body isn't kept.
	_keptBodyLimit = 1 << 20

	// _idleConnTimeout closes a connection to the target that stays idle, as the forward proxy does. A request still
	// being served when its mock leaves returns its connection afterwards: it closes then, at the latest.
	_idleConnTimeout = 15 * time.Second
)

type FromTypes interface{ string | *url.URL }

// ProxyReply represents a response stub that will be the response "proxied" from the specified target.
// Use From to init a new ProxyReply. It is a ReplierFactory: each mock built with it gets a proxy of its own. It sends
// through the reply's Transport, or else the server's Config.Transport, or else a transport of its own, whose idle
// connections close when the mock leaves the server or the server closes.
type ProxyReply struct {
	target               *url.URL
	transport            http.RoundTripper
	headers              http.Header
	proxyHeaders         http.Header
	proxyHeadersToRemove []string
	trimPrefix           string
	trimSuffix           string
	timeout              time.Duration
	sslVerify            bool
}

// From creates a ProxyReply with the given target. The target's path and query come before the request's: with
// http://api/v1?tenant=a, a request to /users?page=2 goes to http://api/v1/users?tenant=a&page=2.
func From[T FromTypes](target T) *ProxyReply {
	var u *url.URL

	switch e := any(target).(type) {
	case string:
		var err error

		u, err = url.Parse(e)
		if err != nil {
			panic(fmt.Errorf("reply_proxy: target: %w", err))
		}
	case *url.URL:
		if e == nil {
			panic(errors.New("reply_proxy: target: nil URL"))
		}

		u = e.Clone()
	}

	return &ProxyReply{
		target:               u,
		headers:              make(http.Header),
		proxyHeaders:         make(http.Header),
		proxyHeadersToRemove: make([]string, 0),
		timeout:              _defaultTimeout,
	}
}

// Header sets an extra response header that will be set after the proxy target replies.
func (r *ProxyReply) Header(key, value string) *ProxyReply {
	r.headers.Add(key, value)
	return r
}

// Headers sets extra response headers that will be set after the proxy target replies.
func (r *ProxyReply) Headers(header http.Header) *ProxyReply {
	for k, v := range header {
		for _, vv := range v {
			r.headers.Add(k, vv)
		}
	}

	return r
}

// ForwardHeader sets an extra header to be sent to the proxy target. A hop-by-hop header, such as Connection or
// Upgrade, fails the build of the mock: a proxy doesn't send it on.
func (r *ProxyReply) ForwardHeader(key, value string) *ProxyReply {
	r.proxyHeaders.Add(key, value)
	return r
}

// ProxyHeaders sets extra headers to be sent to the proxy target. A hop-by-hop header, such as Connection or Upgrade,
// fails the build of the mock: a proxy doesn't send it on.
func (r *ProxyReply) ProxyHeaders(header http.Header) *ProxyReply {
	for k, v := range header {
		for _, vv := range v {
			r.proxyHeaders.Add(k, vv)
		}
	}

	return r
}

// RemoveProxyHeaders removes the given header before sending the request to the proxy target.
func (r *ProxyReply) RemoveProxyHeaders(header ...string) *ProxyReply {
	r.proxyHeadersToRemove = append(r.proxyHeadersToRemove, header...)
	return r
}

// TrimPrefix removes the given prefix from the request path, before the target's path is added. It trims text, not
// segments: "/api" turns "/apiary" into "/ary".
func (r *ProxyReply) TrimPrefix(prefix string) *ProxyReply {
	r.trimPrefix = prefix
	return r
}

// TrimSuffix removes the given suffix from the request path, before the target's path is added.
func (r *ProxyReply) TrimSuffix(suffix string) *ProxyReply {
	r.trimSuffix = suffix
	return r
}

// Timeout sets how long to wait for the response headers of the target, from the call: connecting and sending the
// request count too. The body then streams to the client as the target sends it, for as long as the client waits.
// 0 waits forever. Defaults to 30s.
func (r *ProxyReply) Timeout(timeout time.Duration) *ProxyReply {
	r.timeout = timeout
	return r
}

// Transport sets the transport that sends the requests to the target, in place of the server's Config.Transport. It is
// the caller's: the proxy never closes it, and SSLVerify doesn't apply to it.
func (r *ProxyReply) Transport(rt http.RoundTripper) *ProxyReply {
	r.transport = rt
	return r
}

// SkipSSLVerify skips the verification of the target's TLS certificate, as a ProxyReply does unless SSLVerify(true).
func (r *ProxyReply) SkipSSLVerify() *ProxyReply {
	r.sslVerify = false
	return r
}

// SSLVerify sets whether the target's TLS certificate is verified. It isn't by default, so a target with a
// self-signed certificate answers: set it for a target that gets credentials.
func (r *ProxyReply) SSLVerify(v bool) *ProxyReply {
	r.sslVerify = v
	return r
}

func (r *ProxyReply) Describe() descriptor.Descriptor {
	return descriptor.New("proxy", map[string]any{
		"target":         r.target.Redacted(),
		"forward_header": r.proxyHeaders.Clone(),
		"header":         r.headers.Clone(),
		"remove_headers": slices.Clone(r.proxyHeadersToRemove),
		"trim_prefix":    r.trimPrefix,
		"trim_suffix":    r.trimSuffix,
		"timeout":        r.timeout.String(),
		"ssl_verify":     r.sslVerify,
	})
}

// NewReplier implements ReplierFactory. Each mock gets a proxy of its own: copies of the target and the headers, and,
// without the reply's Transport or the server's, a transport of its own.
func (r *ProxyReply) NewReplier(app *Server) (Replier, error) {
	if r.timeout < 0 {
		return nil, fmt.Errorf("reply_proxy: timeout must be 0 or more, not %s", r.timeout)
	}

	if t := r.target; (t.Scheme != "http" && t.Scheme != "https") || t.Host == "" {
		return nil, fmt.Errorf("reply_proxy: target %q: expected an absolute http or https URL, such as http://api.local:8080", t.Redacted())
	}

	for name := range r.proxyHeaders {
		if slices.ContainsFunc(_forbiddenHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			return nil, fmt.Errorf("reply_proxy: forward header %q is hop-by-hop: a proxy doesn't send it on", name)
		}
	}

	p := &proxyReplier{
		target:               r.target.Clone(),
		headers:              r.headers.Clone(),
		proxyHeaders:         r.proxyHeaders.Clone(),
		proxyHeadersToRemove: slices.Clone(r.proxyHeadersToRemove),
		trimPrefix:           r.trimPrefix,
		trimSuffix:           r.trimSuffix,
		timeout:              r.timeout,
		// A transport of the caller's can be shared: the proxy never closes it.
		roundTripper: cmp.Or(r.transport, app.config.Transport),
	}

	if p.roundTripper == nil {
		p.transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !r.sslVerify,
				RootCAs:            app.config.TLSClientCAs,
				Certificates:       app.config.TLSCertificates,
			},
			DisableCompression: true,
			IdleConnTimeout:    _idleConnTimeout,
		}
		p.roundTripper = p.transport
	}

	return p, nil
}

// proxyReplier sends the requests of one mock to the target, and replies with the response of the target.
type proxyReplier struct {
	target               *url.URL
	headers              http.Header
	proxyHeaders         http.Header
	proxyHeadersToRemove []string
	trimPrefix           string
	trimSuffix           string
	timeout              time.Duration
	roundTripper         http.RoundTripper
	transport            *http.Transport // roundTripper, when the proxy made it
}

// Reply implements Replier.
func (p *proxyReplier) Reply(_ context.Context, input ReplyInput) (*Response, error) {
	return p.build(input.RequestValues)
}

// Close releases the idle connections to the target, when the proxy made the transport that holds them. The transport
// stays usable: a request that was being served when the mock left still gets its answer.
func (p *proxyReplier) Close() error {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}

	return nil
}

func (p *proxyReplier) build(req *RequestValues) (*Response, error) {
	// The trims match the decoded path and cut the escaped path at the same place, so escapes such as "%2F" stay.
	path, escaped := req.RawRequest.URL.Path, req.RawRequest.URL.EscapedPath()

	if rest, ok := strings.CutPrefix(path, p.trimPrefix); ok {
		escaped = escaped[escapedIndex(escaped, len(p.trimPrefix)):]
		path = rest
	}

	if rest, ok := strings.CutSuffix(path, p.trimSuffix); ok {
		escaped = escaped[:escapedIndex(escaped, len(rest))]
		path = rest
	}

	// What the trims leave starts with "/", unless they left nothing: "/api/users" cut by "/api/" goes as "/users". A
	// "/" that came from an escape, as in "/api%2Fv1" cut by "/api", goes as "/": sent as "%2Fv1", the target would
	// not start with "/". The escapes after it stay.
	switch {
	case path == "":
	case !strings.HasPrefix(path, "/"):
		path, escaped = "/"+path, "/"+escaped
	case !strings.HasPrefix(escaped, "/"):
		escaped = "/" + escaped[len("%2F"):]
	}

	// The target's path comes first, with one "/" between them.
	base, baseEscaped := p.target.Path, p.target.EscapedPath()
	if path != "" && strings.HasSuffix(baseEscaped, "/") {
		base, baseEscaped = base[:len(base)-1], baseEscaped[:len(baseEscaped)-1]
	}

	path, escaped = cmp.Or(base+path, "/"), cmp.Or(baseEscaped+escaped, "/")

	// The timeout covers the wait for the response headers. The call ends when its body is closed.
	ctx, cancel := context.WithCancelCause(req.RawRequest.Context())

	// The target gets a copy: what runs after the reply, such as callbacks and events, sees the request as it came.
	out := req.RawRequest.Clone(ctx)
	out.URL.Host = p.target.Host
	out.URL.Scheme = p.target.Scheme
	out.URL.Path, out.URL.RawPath = path, escaped
	out.Host = p.target.Host
	out.RequestURI = ""

	// The target's query comes first too.
	switch {
	case out.URL.RawQuery == "":
		out.URL.RawQuery = p.target.RawQuery
	case p.target.RawQuery != "":
		out.URL.RawQuery = p.target.RawQuery + "&" + out.URL.RawQuery
	}

	// The client's hop-by-hop headers, and its "Connection: close", are about its connection to the server.
	out.Close = false
	removeRequestHopByHop(out.Header)

	for _, h := range p.proxyHeadersToRemove {
		out.Header.Del(h)
	}

	for key, values := range p.proxyHeaders {
		for _, value := range values {
			out.Header.Add(key, value)
		}
	}

	// As http.Client does, the target's user info goes as Basic auth, unless the request has an Authorization.
	if user := p.target.User; user != nil && out.Header.Get(HeaderAuthorization) == "" {
		password, _ := user.Password()
		out.SetBasicAuth(user.Username(), password)
	}

	var timer *time.Timer
	if p.timeout > 0 {
		timer = time.AfterFunc(p.timeout, func() { cancel(errNoHeaders) })
	}

	res, err := p.roundTripper.RoundTrip(out)

	// The timer fired, or is firing: whatever RoundTrip returned, the call is over. Cancelling here, before reading
	// anything, doesn't wait for the timer's own cancel.
	if timer != nil && !timer.Stop() {
		cancel(errNoHeaders)

		if err == nil {
			res.Body.Close()
		}

		return nil, fmt.Errorf("reply_proxy: calling the target %s: no response headers within %s", p.target.Redacted(), p.timeout)
	}

	if err != nil {
		cancel(nil)
		return nil, fmt.Errorf("reply_proxy: calling the target %s: %w", p.target.Redacted(), err)
	}

	stub := newResponse()

	removeHopByHop(res.Header)

	for k, v := range res.Header {
		for _, vv := range v {
			stub.Header.Add(k, vv)
		}
	}

	for key, values := range p.headers {
		for _, value := range values {
			stub.Header.Add(key, value)
		}
	}

	stub.StatusCode = res.StatusCode
	stub.BodyCloser = &targetBody{ReadCloser: res.Body, cancel: cancel}
	stub.proxied = true

	// A body of unknown length, or an event stream, goes to the client part by part, as the target sends it. Other
	// bodies fill the server's buffer, which sends them as it fills.
	stub.flush = res.ContentLength < 0 || mediaType(res.Header.Get(HeaderContentType)) == "text/event-stream"

	// The target's trailers: the keys it announced now, and their values once its body is read to the end.
	if res.Trailer == nil {
		res.Trailer = make(http.Header)
	}

	stub.Trailer = res.Trailer

	return stub, nil
}

// targetBody is the body of a target's response, which streams to the client. It keeps what it reads, up to
// _keptBodyLimit, and closing it ends the call to the target.
type targetBody struct {
	io.ReadCloser

	cancel context.CancelCauseFunc
	kept   []byte
	over   bool // read more than it keeps
	eof    bool
}

// Read reads the body, and keeps what it read.
func (b *targetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)

	switch {
	case b.over:
	case len(b.kept)+n > _keptBodyLimit:
		b.over, b.kept = true, nil
	default:
		b.kept = append(b.kept, p[:n]...)
	}

	if errors.Is(err, io.EOF) {
		b.eof = true
	}

	return n, err
}

// Close closes the body and ends the call to the target.
func (b *targetBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel(nil)

	return err
}

// Kept returns the body, once read to its end, when it fits in _keptBodyLimit.
func (b *targetBody) Kept() ([]byte, bool) {
	return b.kept, b.eof && !b.over
}

// removeHopByHop removes the hop-by-hop headers from h: the ones its Connection header names, and _forbiddenHeaders.
func removeHopByHop(h http.Header) {
	for _, v := range h.Values(HeaderConnection) {
		for name := range strings.SplitSeq(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}

	for _, name := range _forbiddenHeaders {
		h.Del(name)
	}
}

// removeRequestHopByHop removes the hop-by-hop headers from h, the header of a request that goes on. A Te that accepts
// trailers stays as "Te: trailers", which HTTP/2 and gRPC targets need.
func removeRequestHopByHop(h http.Header) {
	trailers := false
	for _, v := range h.Values("Te") {
		for token := range strings.SplitSeq(v, ",") {
			trailers = trailers || strings.EqualFold(strings.TrimSpace(token), "trailers")
		}
	}

	removeHopByHop(h)

	if trailers {
		h.Set("Te", "trailers")
	}
}

// escapedIndex returns the index in escaped, a validly escaped path, where its first n decoded bytes end. Each "%"
// starts an escape: three bytes that decode to one.
func escapedIndex(escaped string, n int) int {
	i := 0
	for range n {
		if escaped[i] == '%' {
			i += 3
		} else {
			i++
		}
	}

	return i
}
