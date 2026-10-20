package mockhttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/internal/config"
)

var (
	_ ProxyConfigurer = (*ProxyConfig)(nil)
	_ Replier         = (*forwardProxy)(nil)
)

// ProxyConfig configures a proxy.
type ProxyConfig struct {
	// Via sets the URL, http or https, of another proxy server to send the requests through, with the transport the
	// proxy makes. Its credentials go in its URL: a client's Proxy-Authorization is for this proxy, and doesn't go on.
	// Over https, set SSLVerify too, so they go to that proxy only. Without Via, the transport the proxy makes goes
	// through the proxy of the environment, HTTP_PROXY, HTTPS_PROXY and NO_PROXY, which leaves out localhost.
	Via string

	// Timeout bounds the wait for the response headers of the host, connecting and sending the request included, with
	// any transport. 0 waits forever. Defaults to 30s, as for proxy replies.
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

var _defaultProxyConfig = ProxyConfig{Timeout: _defaultTimeout, SSLVerify: false}

// forwardProxy sends the requests no mock matches to their hosts: it answers a request in absolute form, which names
// its host, with the host's response, and opens a tunnel to the host of a CONNECT.
type forwardProxy struct {
	upstream

	id     string // of the server: see Server.id
	logger *zerolog.Logger
}

// newProxy returns the forward proxy of conf. Via and SSLVerify shape the transport the proxy makes, so they fail it
// with a transport of the caller's, which would ignore them.
func newProxy(id string, log *zerolog.Logger, conf *Config) (*forwardProxy, error) {
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

		return &forwardProxy{rt: rt, timeout: conf.Proxy.Timeout, id: id, logger: log}, nil
	}

	transport := newTransport(conf, conf.Proxy.SSLVerify, via)

	return &forwardProxy{rt: transport, owned: transport, timeout: conf.Proxy.Timeout, id: id, logger: log}, nil
}

// Reply implements Replier: it sends the request, in absolute form, to its host, and answers with the host's response.
func (p *forwardProxy) Reply(_ context.Context, input ReplyInput) (*Response, error) {
	in := input.RequestValues.RawRequest

	// The host gets a copy: the middlewares that read the request after the proxy see it as it came.
	out, err := forwardRequest(in.Context(), in, p.id)
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}

	res, err := p.send(out, displayURL(in.URL))
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}

	return res, nil
}

// tunnel opens a tunnel to the host of r, a CONNECT. It returns an error when the tunnel didn't open, or broke off.
func (p *forwardProxy) tunnel(w http.ResponseWriter, r *http.Request) error {
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
