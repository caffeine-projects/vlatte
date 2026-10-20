package mockhttp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

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
	// any transport, and the opening of a tunnel. 0 sets no bound of its own: the transport the proxy makes, and the
	// tunnels, still give up connecting after 30s, and a TLS handshake after 10s. Setup().Proxy() and the settings
	// default it to 30s, as for proxy replies, but a ProxyConfig given to Setup().Proxy sets every field: one without
	// Timeout has 0.
	Timeout time.Duration

	// SSLVerify sets whether the transport the proxy makes, and the tunnels, verify the TLS certificates of the hosts,
	// and of an https proxy on the way, Via or the one of the environment. They aren't verified by default.
	SSLVerify bool

	// Transport sends the requests the proxy passes on, in place of the server's Config.Transport. It is the caller's:
	// the server never closes it. With neither, the proxy makes a transport, which Via, Timeout and SSLVerify shape:
	// Via and SSLVerify fail New with a transport of the caller's, which would ignore them. The tunnels of CONNECT
	// requests go through the proxy the transport the proxy makes picks, Via or the proxy of the environment, and dial
	// their hosts with a transport of the caller's.
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
// its host, with the host's response, and dials the host of a CONNECT for a tunnel.
type forwardProxy struct {
	upstream

	id               string        // of the server: see Server.id
	dialer           *net.Dialer   // of the tunnels, to their hosts or to the proxy on the way
	handshakeTimeout time.Duration // bounds the TLS handshake of a tunnel with an https proxy on the way
}

// newProxy returns the forward proxy of conf. Via and SSLVerify shape the transport the proxy makes, so they fail it
// with a transport of the caller's, which would ignore them.
func newProxy(id string, conf *Config) (*forwardProxy, error) {
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

		return &forwardProxy{
			rt:               rt,
			timeout:          conf.Proxy.Timeout,
			id:               id,
			dialer:           newDialer(),
			handshakeTimeout: _tlsHandshakeTimeout,
		}, nil
	}

	transport := newTransport(conf, conf.Proxy.SSLVerify, via)

	return &forwardProxy{
		rt:               transport,
		owned:            transport,
		timeout:          conf.Proxy.Timeout,
		id:               id,
		dialer:           newDialer(),
		handshakeTimeout: _tlsHandshakeTimeout,
	}, nil
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
