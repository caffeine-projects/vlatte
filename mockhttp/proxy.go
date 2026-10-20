package mockhttp

import (
	"cmp"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

var _ ProxyConfigurer = (*ProxyConfig)(nil)

// ProxyConfig configures a proxy.
type ProxyConfig struct {
	// Via sets a URL to route the request via another proxy server, with the transport the proxy makes. Its credentials
	// go in its URL: a client's Proxy-Authorization is for this proxy, and doesn't go on. Over https, set SSLVerify too,
	// so they go to that proxy only.
	Via string

	// Timeout bounds the wait for the response headers of the host, with the transport the proxy makes.
	Timeout time.Duration

	// SSLVerify enable/disable server certificate verification, with the transport the proxy makes.
	SSLVerify bool

	// Transport sends the requests the proxy passes on, in place of the server's Config.Transport. It is the caller's:
	// the server never closes it. With neither, the proxy makes a transport, which Via, Timeout and SSLVerify shape.
	Transport http.RoundTripper
}

// ProxyConfigurer lets users configure the proxy.
type ProxyConfigurer interface {
	Apply(config *ProxyConfig) error
}

// Apply allows ProxyConfig to be used as a Configurer.
func (p *ProxyConfig) Apply(c *ProxyConfig) error {
	c.Transport = p.Transport
	c.Timeout = p.Timeout
	c.Via = p.Via
	c.SSLVerify = p.SSLVerify

	return nil
}

var _defaultProxyConfig = ProxyConfig{Timeout: 10 * time.Second, SSLVerify: false}

type reverseProxy struct {
	roundTripper http.RoundTripper
	transport    *http.Transport // roundTripper, when the proxy made it
}

func newProxy(config *Config) *reverseProxy {
	rt := cmp.Or(config.Proxy.Transport, config.Transport)
	if rt == nil {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: !config.Proxy.SSLVerify,
				RootCAs:            config.TLSClientCAs,
				Certificates:       config.TLSCertificates,
			},
		}

		if config.Proxy.Via != "" {
			u, err := url.Parse(config.Proxy.Via)
			if err != nil {
				panic(fmt.Errorf("proxy: failed to parse proxy via url %s: %w", config.Proxy.Via, err))
			}

			transport.Proxy = http.ProxyURL(u)
		}

		transport.TLSHandshakeTimeout = 15 * time.Second
		transport.IdleConnTimeout = 15 * time.Second
		transport.ExpectContinueTimeout = 1 * time.Second
		transport.ResponseHeaderTimeout = config.Proxy.Timeout

		return &reverseProxy{roundTripper: transport, transport: transport}
	}

	return &reverseProxy{roundTripper: rt}
}

// serve sends r to its host and answers with the host's response, or opens a tunnel for a CONNECT. It returns the
// status and headers the client got, or an error when the client didn't get the host's whole answer.
func (p *reverseProxy) serve(w http.ResponseWriter, r *http.Request) (*Response, error) {
	if r.Method == http.MethodConnect {
		return p.handleTunneling(w, r)
	}

	return p.handleHTTP(w, r)
}

// close releases the idle connections of the transport the proxy made. A ProxyConfig.Transport is the caller's.
func (p *reverseProxy) close() {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}
}

func (p *reverseProxy) handleTunneling(w http.ResponseWriter, r *http.Request) (*Response, error) {
	// Dial before hijacking: once the connection is hijacked, w can no longer report errors.
	out, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return nil, err
	}

	defer out.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy: hijacking is not supported", http.StatusInternalServerError)
		return nil, errors.New("proxy: hijacking is not supported")
	}

	in, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return nil, err
	}

	defer in.Close()

	// WriteHeader has no effect on a hijacked connection, so the tunnel response is written directly.
	if _, err = io.WriteString(in, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return nil, fmt.Errorf("proxy: establishing the tunnel: %w", err)
	}

	errCh := make(chan error, 2)
	cp := func(dst io.WriteCloser, src io.ReadCloser) {
		_, err := io.Copy(dst, src)
		errCh <- err
	}

	go cp(out, in)
	go cp(in, out)

	if err = <-errCh; err != nil {
		return nil, fmt.Errorf("proxy: tunnel: %w", err)
	}

	return &Response{StatusCode: http.StatusOK}, nil
}

func (p *reverseProxy) handleHTTP(w http.ResponseWriter, r *http.Request) (*Response, error) {
	// The host gets a copy: the middlewares that read r after the proxy see the request as it came.
	res, err := p.roundTripper.RoundTrip(forwardRequest(r.Context(), r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return nil, err
	}

	defer res.Body.Close()

	removeHopByHop(res.Header)
	dropCORSHeaders(w, res.Header)

	for k, vv := range res.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(res.StatusCode)

	if _, err = io.Copy(w, res.Body); err != nil {
		return nil, fmt.Errorf("proxy: copying the body of a %d response: %w", res.StatusCode, err)
	}

	for k, vv := range res.Trailer {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	return &Response{StatusCode: res.StatusCode, Header: res.Header, Trailer: res.Trailer}, nil
}
