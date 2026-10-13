package mockhttp

import (
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
)

var _forbiddenHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

const _defaultTimeout = 30 * time.Second

type FromTypes interface{ string | *url.URL }

// ProxyReply represents a response stub that will be the response "proxied" from the specified target.
// Use From to init a new ProxyReply. It is a ReplierFactory: each mock built with it gets a proxy of its own, with an
// HTTP client of its own.
type ProxyReply struct {
	target               *url.URL
	headers              http.Header
	proxyHeaders         http.Header
	proxyHeadersToRemove []string
	trimPrefix           string
	trimSuffix           string
	timeout              time.Duration
	noFollow             bool
	sslVerify            bool
}

// From creates a ProxyReply with the given target.
func From[T FromTypes](target T) *ProxyReply {
	u := &url.URL{}

	switch e := any(target).(type) {
	case string:
		var err error

		u, err = url.Parse(e)
		if err != nil {
			panic(fmt.Errorf("reply_proxy: target: %w", err))
		}
	case *url.URL:
		u = e
	}

	return &ProxyReply{
		target:               u,
		headers:              make(http.Header),
		proxyHeaders:         make(http.Header),
		proxyHeadersToRemove: make([]string, 0),
		timeout:              _defaultTimeout,
	}
}

// NoFollow has no effect and is kept for compatibility: a ProxyReply never follows redirects.
// Redirect responses from the target are passed to the client unchanged.
func (r *ProxyReply) NoFollow() *ProxyReply {
	r.noFollow = true
	return r
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

// ForwardHeader sets an extra header to be sent to the proxy target.
func (r *ProxyReply) ForwardHeader(key, value string) *ProxyReply {
	r.proxyHeaders.Add(key, value)
	return r
}

// ProxyHeaders sets extra headers to be sent to the proxy target.
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

// TrimPrefix removes the given prefix from the URL before proxying the request.
func (r *ProxyReply) TrimPrefix(prefix string) *ProxyReply {
	r.trimPrefix = prefix
	return r
}

// TrimSuffix removes the given suffix from the URL before proxying the request.
func (r *ProxyReply) TrimSuffix(suffix string) *ProxyReply {
	r.trimSuffix = suffix
	return r
}

// Timeout sets the timeout for the target HTTP request.
// Defaults to 30s.
func (r *ProxyReply) Timeout(timeout time.Duration) *ProxyReply {
	r.timeout = timeout
	return r
}

// SkipSSLVerify skips server certificate verification.
func (r *ProxyReply) SkipSSLVerify() *ProxyReply {
	r.sslVerify = false
	return r
}

// SSLVerify sets if the client should verify server certificate.
func (r *ProxyReply) SSLVerify(v bool) *ProxyReply {
	r.sslVerify = v
	return r
}

func noFollow(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func (r *ProxyReply) Describe() descriptor.Descriptor {
	return descriptor.New("proxy", map[string]any{
		"target":         r.target.String(),
		"forward_header": r.proxyHeaders.Clone(),
		"header":         r.headers.Clone(),
		"remove_headers": slices.Clone(r.proxyHeadersToRemove),
		"trim_prefix":    r.trimPrefix,
		"trim_suffix":    r.trimSuffix,
		"timeout":        r.timeout.String(),
		"ssl_verify":     r.sslVerify,
		"no_follow":      r.noFollow,
	})
}

// NewReplier implements ReplierFactory. Each mock gets a proxy of its own: copies of the target and the headers, and an
// HTTP client of its own. With Config.HTTPClientFactory, the client is a copy of the one the factory returns, which can
// be a client it shares, such as http.DefaultClient.
func (r *ProxyReply) NewReplier(app *Server) (Replier, error) {
	p := &proxyReplier{
		target:               r.target.Clone(),
		headers:              r.headers.Clone(),
		proxyHeaders:         r.proxyHeaders.Clone(),
		proxyHeadersToRemove: slices.Clone(r.proxyHeadersToRemove),
		trimPrefix:           r.trimPrefix,
		trimSuffix:           r.trimSuffix,
		timeout:              r.timeout,
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: !r.sslVerify,
		RootCAs:            app.config.TLSClientCAs,
		Certificates:       app.config.TLSCertificates,
	}

	if app.config.HTTPClientFactory == nil {
		p.client = &http.Client{Transport: &http.Transport{DisableCompression: true, TLSClientConfig: tlsConfig}}
	} else {
		client := app.config.HTTPClientFactory()
		if client == nil {
			return nil, errors.New("reply_proxy: the HTTP client factory returned no client")
		}

		// A copy: the factory can return a client it shares, and the lines below change the client.
		p.client = new(*client)
	}

	if p.client.Transport == nil {
		p.client.Transport = &http.Transport{TLSClientConfig: tlsConfig, DisableCompression: true}
	}

	if r.noFollow {
		p.client.CheckRedirect = noFollow
	}

	app.closeIdleConnsOnClose(p.client.CloseIdleConnections)

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
	client               *http.Client
}

// Reply implements Replier.
func (p *proxyReplier) Reply(_ context.Context, input ReplyInput) (*Response, error) {
	return p.build(input.RequestValues)
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

	// The "/" that starts the path came from an escape, as in "/api%2Fv1" cut by "/api": sent as "%2Fv1", the target
	// would not start with "/". The path goes as Path escapes it.
	if strings.HasPrefix(path, "/") && !strings.HasPrefix(escaped, "/") {
		escaped = ""
	}

	req.RawRequest.URL.Host = p.target.Host
	req.RawRequest.URL.Scheme = p.target.Scheme
	req.RawRequest.URL.Path, req.RawRequest.URL.RawPath = path, escaped
	req.RawRequest.Host = p.target.Host
	req.RawRequest.RequestURI = ""

	for _, h := range p.proxyHeadersToRemove {
		req.RawRequest.Header.Del(h)
	}

	for key, values := range p.proxyHeaders {
		for _, value := range values {
			req.RawRequest.Header.Add(key, value)
		}
	}

	ctx, cancel := context.WithTimeout(req.RawRequest.Context(), p.timeout)
	defer cancel()

	res, err := p.client.Transport.RoundTrip(req.RawRequest.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("reply_proxy: calling the target %s: %w", p.target, err)
	}

	defer res.Body.Close()

	stub := newResponse()

	for _, h := range _forbiddenHeaders {
		res.Header.Del(h)
	}

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

	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	stub.Body = b
	stub.Trailer = make(http.Header, len(res.Trailer))

	for key, values := range res.Trailer {
		for _, value := range values {
			stub.Trailer.Add(key, value)
		}
	}

	return stub, nil
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
