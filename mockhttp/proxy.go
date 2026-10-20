package mockhttp

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/internal/config"
)

var _ ProxyConfigurer = (*ProxyConfig)(nil)

// ProxyConfig configures a proxy.
type ProxyConfig struct {
	// Via sets the URL, http or https, of another proxy server to send the requests through, with the transport the
	// proxy makes. Its credentials go in its URL: a client's Proxy-Authorization is for this proxy, and doesn't go on.
	// Over https, set SSLVerify too, so they go to that proxy only. Without Via, the transport the proxy makes goes
	// through the proxy of the environment, HTTP_PROXY, HTTPS_PROXY and NO_PROXY, which leaves out localhost.
	Via string

	// Timeout bounds the wait for the response headers of the host, with the transport the proxy makes.
	Timeout time.Duration

	// SSLVerify enable/disable server certificate verification, with the transport the proxy makes.
	SSLVerify bool

	// Transport sends the requests the proxy passes on, in place of the server's Config.Transport. It is the caller's:
	// the server never closes it. With neither, the proxy makes a transport, which Via, Timeout and SSLVerify shape:
	// Via and SSLVerify fail New with a transport of the caller's, which would ignore them.
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
	logger       *zerolog.Logger
	roundTripper http.RoundTripper
	transport    *http.Transport // roundTripper, when the proxy made it
}

// newProxy returns the forward proxy of conf. Via and SSLVerify shape the transport the proxy makes, so they fail it
// with a transport of the caller's, which would ignore them.
func newProxy(log *zerolog.Logger, conf *Config) (*reverseProxy, error) {
	via, err := parseVia(conf.Proxy.Via)
	if err != nil {
		return nil, fmt.Errorf("proxy: via %q: %w", config.Display(conf.Proxy.Via), err)
	}

	if rt := cmp.Or(conf.Proxy.Transport, conf.Transport); rt != nil {
		switch {
		case via != nil:
			return nil, errors.New("proxy: via has no effect with a transport of the caller's, " +
				"ProxyConfig.Transport or Config.Transport: set the proxy on that transport, or leave via out")
		case conf.Proxy.SSLVerify:
			return nil, errors.New("proxy: ssl_verify has no effect with a transport of the caller's, " +
				"ProxyConfig.Transport or Config.Transport: verify the hosts with that transport, or leave ssl_verify out")
		}

		return &reverseProxy{logger: log, roundTripper: rt}, nil
	}

	transport := newTransport(conf, conf.Proxy.SSLVerify, via)
	transport.ResponseHeaderTimeout = conf.Proxy.Timeout

	return &reverseProxy{logger: log, roundTripper: transport, transport: transport}, nil
}

// serve sends r to its host and answers with the host's response, or opens a tunnel for a CONNECT. It returns an
// error when the client didn't get the host's whole answer.
func (p *reverseProxy) serve(w http.ResponseWriter, r *http.Request) error {
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

func (p *reverseProxy) handleTunneling(w http.ResponseWriter, r *http.Request) error {
	// Dial before hijacking: once the connection is hijacked, w can no longer report errors.
	out, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return err
	}

	defer out.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy: hijacking is not supported", http.StatusInternalServerError)
		return errors.New("proxy: hijacking is not supported")
	}

	in, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return err
	}

	defer in.Close()

	// WriteHeader has no effect on a hijacked connection, so the tunnel response is written directly.
	if _, err = io.WriteString(in, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		p.logger.Error().Err(err).Str("url", r.URL.String()).Msg("proxy: error establishing tunnel")
		return err
	}

	errCh := make(chan error, 2)
	cp := func(dst io.WriteCloser, src io.ReadCloser) {
		_, err := io.Copy(dst, src)
		errCh <- err
	}

	go cp(out, in)
	go cp(in, out)

	err = <-errCh
	if err != nil {
		p.logger.Error().Err(err).
			Str("url", r.URL.String()).
			Str("method", r.Method).
			Msg("proxy: error writing response")
	}

	return err
}

func (p *reverseProxy) handleHTTP(w http.ResponseWriter, r *http.Request) error {
	// The host gets a copy: the middlewares that read r after the proxy see the request as it came.
	res, err := p.roundTripper.RoundTrip(forwardRequest(r.Context(), r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return err
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

	_, err = io.Copy(w, res.Body)
	if err != nil {
		p.logger.Error().Err(err).
			Str("url", r.URL.String()).
			Str("method", r.Method).
			Str("status", res.Status).
			Msg("proxy: error copying response body")

		return err
	}

	for k, vv := range res.Trailer {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	return nil
}
