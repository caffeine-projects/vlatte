package mockhttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
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

// FromTypes are the types of the target From takes: a URL, as text, of a string type of any name, or parsed.
type FromTypes interface{ ~string | *url.URL }

// ProxyReply represents a response stub that will be the response "proxied" from the specified target.
// Use From to init a new ProxyReply. It is a ReplierFactory: each mock built with it gets a proxy of its own. It sends
// through the reply's Transport, or else the server's Config.Transport, or else a transport of its own, whose idle
// connections close when the mock leaves the server or the server closes.
type ProxyReply struct {
	target               *url.URL
	transport            http.RoundTripper
	headers              http.Header
	requestHeaders       http.Header
	removeRequestHeaders []string
	trimPrefix           string
	trimSuffix           string
	timeout              time.Duration
	sslVerify            bool
	sslVerifySet         bool // SSLVerify or SkipSSLVerify was called: a transport of the caller's would ignore it
	xForwarded           bool
	preserveHost         bool
}

// From creates a ProxyReply with the given target. The target's path and query come before the request's: with
// http://api/v1?tenant=a, a request to /users?page=2 goes to http://api/v1/users?tenant=a&page=2.
func From[T FromTypes](target T) *ProxyReply {
	var u *url.URL

	if e, ok := any(target).(*url.URL); ok {
		if e == nil {
			panic(errors.New("reply_proxy: target: nil URL"))
		}

		u = e.Clone()
	} else {
		// A string type of another name, such as type Target string, is no string to a type switch.
		var err error

		u, err = url.Parse(reflect.ValueOf(target).String())
		if err != nil {
			panic(fmt.Errorf("reply_proxy: target: %w", err))
		}
	}

	return &ProxyReply{
		target:               u,
		headers:              make(http.Header),
		requestHeaders:       make(http.Header),
		removeRequestHeaders: make([]string, 0),
		timeout:              _defaultTimeout,
	}
}

// ProxyAll returns a mock that answers the requests no other mock matches with reply, as WireMock's proxy-all does: it
// matches any method and path, and comes last, at the priority math.MaxInt. It leaves alone what the forward proxy
// sends on to its host, CONNECTs and requests in absolute form, which name it.
func ProxyAll(reply *ProxyReply) *Builder {
	return AnyMethod().
		Priority(math.MaxInt).
		RequestMatches(func(r *http.Request) (bool, error) {
			return r.Method != http.MethodConnect && r.URL.Host == "", nil
		}).
		Reply(reply)
}

// Header adds a value to a response header, which replaces the target's values of that name.
func (r *ProxyReply) Header(key, value string) *ProxyReply {
	r.headers.Add(key, value)
	return r
}

// Headers adds the values of response headers, which replace the target's values of those names.
func (r *ProxyReply) Headers(header http.Header) *ProxyReply {
	for k, v := range header {
		for _, vv := range v {
			r.headers.Add(k, vv)
		}
	}

	return r
}

// RequestHeader adds a value to a header sent to the proxy target, which replaces the client's values of that name. A
// hop-by-hop header, such as Connection or Upgrade, fails the build of the mock: it is about one connection, and a
// proxy doesn't make one up. A client's request to switch protocols goes on as the client asked. "Te: trailers" is
// the exception.
func (r *ProxyReply) RequestHeader(key, value string) *ProxyReply {
	r.requestHeaders.Add(key, value)
	return r
}

// RequestHeaders adds the values of headers sent to the proxy target, which replace the client's values of those names.
// A hop-by-hop header, such as Connection or Upgrade, fails the build of the mock, as for RequestHeader.
func (r *ProxyReply) RequestHeaders(header http.Header) *ProxyReply {
	for k, v := range header {
		for _, vv := range v {
			r.requestHeaders.Add(k, vv)
		}
	}

	return r
}

// RemoveRequestHeaders removes the given headers from the request before it is sent to the proxy target.
func (r *ProxyReply) RemoveRequestHeaders(header ...string) *ProxyReply {
	r.removeRequestHeaders = append(r.removeRequestHeaders, header...)
	return r
}

// TrimPrefix removes the given prefix from the request path, before the target's path is added. It trims whole
// segments: "/api" turns "/api/users" into "/users", and "/api" into "", and leaves "/apiary" as it is.
func (r *ProxyReply) TrimPrefix(prefix string) *ProxyReply {
	r.trimPrefix = prefix
	return r
}

// TrimSuffix removes the given suffix from the request path, before the target's path is added. It trims text, such
// as an extension: ".json" turns "/users.json" into "/users".
func (r *ProxyReply) TrimSuffix(suffix string) *ProxyReply {
	r.trimSuffix = suffix
	return r
}

// XForwarded sets the X-Forwarded-For, X-Forwarded-Host and X-Forwarded-Proto headers of the requests the target gets,
// as httputil.ProxyRequest.SetXForwarded does: the client's address, after those of the proxies the request came
// through, the host the client asked for, and its scheme. Without it, the target gets the client's, if any.
func (r *ProxyReply) XForwarded() *ProxyReply {
	r.xForwarded = true
	return r
}

// PreserveHost sends the target the Host the client asked for, in place of the target's own, as for a target that
// serves several hosts, or that checks which host it is asked for.
func (r *ProxyReply) PreserveHost() *ProxyReply {
	r.preserveHost = true
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

// SSLVerify sets whether the target's TLS certificate is verified, and the certificate of an https proxy on the way,
// from HTTPS_PROXY. They aren't by default, so a target with a self-signed certificate answers: set it for a target
// that gets credentials, such as https://user:pass@api.
func (r *ProxyReply) SSLVerify(v bool) *ProxyReply {
	r.sslVerify, r.sslVerifySet = v, true
	return r
}

// Describe describes the reply in the mock file format. The target is described as it was set, its credentials
// included, so the description loads back as the same reply: errors and logs hide them. ssl_verify is described only
// when it was set, so the description loads back on a server whose Config.Transport would refuse it.
func (r *ProxyReply) Describe() descriptor.Descriptor {
	props := map[string]any{
		"target":                 r.target.String(),
		"request_header":         r.requestHeaders.Clone(),
		"header":                 r.headers.Clone(),
		"remove_request_headers": slices.Clone(r.removeRequestHeaders),
		"trim_prefix":            r.trimPrefix,
		"trim_suffix":            r.trimSuffix,
		"timeout":                r.timeout.String(),
	}

	if r.sslVerifySet {
		props["ssl_verify"] = r.sslVerify
	}

	if r.xForwarded {
		props["x_forwarded"] = true
	}

	if r.preserveHost {
		props["preserve_host"] = true
	}

	return descriptor.New("proxy", props)
}

// NewReplier implements ReplierFactory. Each mock gets a proxy of its own: copies of the target and the headers, and,
// without the reply's Transport or the server's, a transport of its own.
func (r *ProxyReply) NewReplier(app *Server) (Replier, error) {
	if r.timeout < 0 {
		return nil, fmt.Errorf("reply_proxy: timeout must be 0 or more, not %s", r.timeout)
	}

	if _, err := checkTargetURL(r.target.String()); err != nil {
		return nil, fmt.Errorf("reply_proxy: target %q: %w", displayURL(r.target), err)
	}

	for name, values := range r.requestHeaders {
		// "Te: trailers" is the one hop-by-hop header a proxy sends on, as for a client's.
		trailers := strings.EqualFold(name, "Te") && !slices.ContainsFunc(values, func(v string) bool { return !strings.EqualFold(v, "trailers") })
		if !trailers && slices.ContainsFunc(_forbiddenHeaders, func(h string) bool { return strings.EqualFold(h, name) }) {
			return nil, fmt.Errorf("reply_proxy: request header %q is hop-by-hop: a proxy doesn't send it on", name)
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
		requestHeaders:       r.requestHeaders.Clone(),
		removeRequestHeaders: slices.Clone(r.removeRequestHeaders),
		trimPrefix:           r.trimPrefix,
		trimSuffix:           r.trimSuffix,
		xForwarded:           r.xForwarded,
		preserveHost:         r.preserveHost,
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
	requestHeaders       http.Header
	removeRequestHeaders []string
	trimPrefix           string
	trimSuffix           string
	xForwarded           bool
	preserveHost         bool
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

	// A mock whose steps or mappers couldn't apply to a connection that switched protocols doesn't ask for a switch.
	if input.noSwitch {
		out.Header.Del(HeaderConnection)
		out.Header.Del(HeaderUpgrade)
	}

	out.URL = target

	out.Host = p.target.Host
	if p.preserveHost {
		out.Host = in.Host
	}

	if p.xForwarded {
		(&httputil.ProxyRequest{In: in, Out: out}).SetXForwarded()
	}

	for _, h := range p.removeRequestHeaders {
		out.Header.Del(h)
	}

	// A header of the mock's replaces the client's values of that name: a mock sets a token for the target in place of
	// the client's, rather than send both.
	for key, values := range p.requestHeaders {
		out.Header[key] = slices.Clone(values)
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

	// A header of the mock's replaces the target's values of that name: the client gets one Content-Type, not two.
	for key, values := range p.headers {
		res.Header[key] = slices.Clone(values)
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

	// The prefix ends at a segment's end, in the escaped path: what follows starts another segment, or nothing does.
	// An escaped "/", "%2F", is inside a segment.
	if rest, ok := strings.CutPrefix(path, p.trimPrefix); ok {
		at := escapedIndex(escaped, len(p.trimPrefix))
		if at == len(escaped) || escaped[at] == '/' || strings.HasSuffix(p.trimPrefix, "/") {
			escaped = escaped[at:]
			path = rest
		}
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
