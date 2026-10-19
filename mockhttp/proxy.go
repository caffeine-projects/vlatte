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

	"github.com/rs/zerolog"
)

var _ ProxyConfigurer = (*ProxyConfig)(nil)

// ProxyConfig configures a proxy.
type ProxyConfig struct {
	// Via sets a URL to route the request via another proxy server, with the transport the proxy makes.
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
	logger       *zerolog.Logger
	roundTripper http.RoundTripper
	transport    *http.Transport // roundTripper, when the proxy made it
}

func newProxy(log *zerolog.Logger, config *Config) *reverseProxy {
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

		return &reverseProxy{logger: log, roundTripper: transport, transport: transport}
	}

	return &reverseProxy{logger: log, roundTripper: rt}
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
	r.Header.Del(HeaderConnection)

	res, err := p.roundTripper.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return err
	}

	defer res.Body.Close()

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
