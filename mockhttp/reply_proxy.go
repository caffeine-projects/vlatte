package mockhttp

import (
	"cmp"
	"context"
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
)

const _defaultTimeout = 30 * time.Second

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
	sslVerifySet         bool // SSLVerify or SkipSSLVerify was called: a transport of the caller's would ignore it
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
// Upgrade, fails the build of the mock: a proxy doesn't send it on. "Te: trailers" is the exception.
func (r *ProxyReply) ForwardHeader(key, value string) *ProxyReply {
	r.proxyHeaders.Add(key, value)
	return r
}

// ProxyHeaders sets extra headers to be sent to the proxy target. A hop-by-hop header, such as Connection or Upgrade,
// fails the build of the mock: a proxy doesn't send it on. "Te: trailers" is the exception.
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
// the caller's: the proxy never closes it. It decides on its own whether it verifies the target, so SSLVerify and
// SkipSSLVerify fail the build of the mock with it, as with the server's Config.Transport.
func (r *ProxyReply) Transport(rt http.RoundTripper) *ProxyReply {
	r.transport = rt
	return r
}

// SkipSSLVerify skips the verification of the target's TLS certificate, as a ProxyReply does unless SSLVerify(true).
func (r *ProxyReply) SkipSSLVerify() *ProxyReply {
	r.sslVerify, r.sslVerifySet = false, true
	return r
}

// SSLVerify sets whether the target's TLS certificate is verified. It isn't by default, so a target with a
// self-signed certificate answers: set it for a target that gets credentials, such as https://user:pass@api.
func (r *ProxyReply) SSLVerify(v bool) *ProxyReply {
	r.sslVerify, r.sslVerifySet = v, true
	return r
}

// Describe describes the reply in the mock file format. The target is described as it was set, its credentials
// included, so the description loads back as the same reply: errors and logs hide them. ssl_verify is described only
// when it was set, so the description loads back on a server whose Config.Transport would refuse it.
func (r *ProxyReply) Describe() descriptor.Descriptor {
	props := map[string]any{
		"target":         r.target.String(),
		"forward_header": r.proxyHeaders.Clone(),
		"header":         r.headers.Clone(),
		"remove_headers": slices.Clone(r.proxyHeadersToRemove),
		"trim_prefix":    r.trimPrefix,
		"trim_suffix":    r.trimSuffix,
		"timeout":        r.timeout.String(),
	}

	if r.sslVerifySet {
		props["ssl_verify"] = r.sslVerify
	}

	return descriptor.New("proxy", props)
}

// NewReplier implements ReplierFactory. Each mock gets a proxy of its own: copies of the target and the headers, and,
// without the reply's Transport or the server's, a transport of its own.
func (r *ProxyReply) NewReplier(app *Server) (Replier, error) {
	if r.timeout < 0 {
		return nil, fmt.Errorf("reply_proxy: timeout must be 0 or more, not %s", r.timeout)
	}

	if t := r.target; (t.Scheme != "http" && t.Scheme != "https") || t.Host == "" {
		return nil, fmt.Errorf("reply_proxy: target %q: expected an absolute http or https URL, such as http://api.local:8080", displayURL(t))
	}

	for name, values := range r.proxyHeaders {
		// "Te: trailers" is the one hop-by-hop header a proxy sends on, as for a client's.
		trailers := strings.EqualFold(name, "Te") && !slices.ContainsFunc(values, func(v string) bool { return !strings.EqualFold(v, "trailers") })
		if !trailers && slices.ContainsFunc(_forbiddenHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			return nil, fmt.Errorf("reply_proxy: forward header %q is hop-by-hop: a proxy doesn't send it on", name)
		}
	}

	// A transport of the caller's can be shared: the proxy never closes it.
	rt := cmp.Or(r.transport, app.config.Transport)
	if rt != nil && r.sslVerifySet {
		return nil, errors.New("reply_proxy: ssl_verify has no effect with a transport of the caller's, " +
			"ProxyReply.Transport or Config.Transport: verify the target with that transport, or leave ssl_verify out")
	}

	p := &proxyReplier{
		rt:                   rt,
		timeout:              r.timeout,
		id:                   app.id,
		target:               r.target.Clone(),
		headers:              r.headers.Clone(),
		proxyHeaders:         r.proxyHeaders.Clone(),
		proxyHeadersToRemove: slices.Clone(r.proxyHeadersToRemove),
		trimPrefix:           r.trimPrefix,
		trimSuffix:           r.trimSuffix,
	}

	if rt == nil {
		p.owned = newTransport(app.config, r.sslVerify, nil)
		p.rt = p.owned
	}

	return p, nil
}

// proxyReplier sends the requests of one mock to the target, and replies with the response of the target.
type proxyReplier struct {
	upstream

	id                   string // of the server, which a request that comes back holds in its VLatte-Via
	target               *url.URL
	headers              http.Header
	proxyHeaders         http.Header
	proxyHeadersToRemove []string
	trimPrefix           string
	trimSuffix           string
}

// Reply implements Replier.
func (p *proxyReplier) Reply(_ context.Context, input ReplyInput) (*Response, error) {
	in := input.RequestValues.RawRequest

	target, err := p.forwardURL(in.URL)
	if err != nil {
		return nil, err
	}

	// The target gets a copy: what runs after the reply, such as callbacks and events, sees the request as it came.
	out, err := forwardRequest(in.Context(), in, p.id)
	if err != nil {
		return nil, fmt.Errorf("reply_proxy: %w", err)
	}

	out.URL = target
	out.Host = p.target.Host

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

	res, err := p.send(out, displayURL(p.target))
	if err != nil {
		return nil, fmt.Errorf("reply_proxy: %w", err)
	}

	for key, values := range p.headers {
		for _, value := range values {
			res.Header.Add(key, value)
		}
	}

	return res, nil
}

// Close releases the idle connections to the target, when the proxy made the transport that holds them. The transport
// stays usable: a request that was being served when the mock left still gets its answer.
func (p *proxyReplier) Close() error {
	p.close()
	return nil
}

// forwardURL returns the URL a request to in goes to: the target's, with the path of in after the target's path, once
// trimmed, and the query of in after the target's query.
func (p *proxyReplier) forwardURL(in *url.URL) (*url.URL, error) {
	// The trims match the decoded path and cut the escaped path at the same place, so escapes such as "%2F" stay.
	path, escaped := in.Path, in.EscapedPath()

	if rest, ok := strings.CutPrefix(path, p.trimPrefix); ok {
		escaped = escaped[escapedIndex(escaped, len(p.trimPrefix)):]
		path = rest
	}

	if rest, ok := strings.CutSuffix(path, p.trimSuffix); ok {
		escaped = escaped[:escapedIndex(escaped, len(rest))]
		path = rest
	}

	// A "." or ".." segment could climb out of the target's path, for a target that resolves them. The decoded path
	// counts, so escapes such as "%2e%2e" and "..%2F" do too, and so do the dot segments some targets see: one with
	// parameters, "..;", or before a backslash, which Windows reads as "/". A target without a path has nothing to
	// leave: the path goes as the client sent it.
	if strings.Trim(p.target.Path, "/") != "" {
		for segment := range strings.FieldsFuncSeq(path, func(c rune) bool { return c == '/' || c == '\\' }) {
			if segment, _, _ = strings.Cut(segment, ";"); segment == "." || segment == ".." {
				return nil, fmt.Errorf(`reply_proxy: path %q has a "." or ".." segment, which could leave the target's path`, in.EscapedPath())
			}
		}
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

	// The target's query comes first too.
	query := in.RawQuery
	switch {
	case query == "":
		query = p.target.RawQuery
	case p.target.RawQuery != "":
		query = p.target.RawQuery + "&" + query
	}

	return &url.URL{
		Scheme:     p.target.Scheme,
		Host:       p.target.Host,
		Path:       cmp.Or(base+path, "/"),
		RawPath:    cmp.Or(baseEscaped+escaped, "/"),
		RawQuery:   query,
		ForceQuery: in.ForceQuery,
	}, nil
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
