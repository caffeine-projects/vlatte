package mockhttp

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"golang.org/x/net/http/httpproxy"

	"github.com/caffeine-projects/vlatte/internal/httpx"
)

// _idleConnTimeout closes a connection to an upstream that stays idle. A request still being served when its mock
// leaves returns its connection afterwards: it closes then, at the latest.
const _idleConnTimeout = 15 * time.Second

const (
	// _tlsHandshakeTimeout bounds a TLS handshake with an upstream, or with an https proxy on the way.
	_tlsHandshakeTimeout = 10 * time.Second
)

// newDialer returns a dialer of the connections the server opens to its upstreams, and to the proxies on the way,
// which follows the outbound rules of conf.
func newDialer(conf *Config) *net.Dialer {
	return httpx.NewDialer(conf.OutboundAllow, conf.OutboundDeny)
}

// newTransport returns a transport the server makes for its upstreams: the targets of proxy replies and of the
// recording, and the hosts of the forward proxy. It dials and shakes hands within the limits of http.DefaultTransport,
// and goes through via, when set, or else through the proxy of the environment (HTTP_PROXY, HTTPS_PROXY and NO_PROXY),
// which leaves out localhost. It reads the environment as it is now: http.ProxyFromEnvironment reads it once in a
// process, so a test that sets it later wouldn't see it. It passes compressed bodies on as they come, and speaks
// HTTP/1.1 to the upstreams. verify sets whether it verifies the TLS certificates of the upstreams, with the server's
// client CAs.
func newTransport(conf *Config, verify bool, via *url.URL) *http.Transport {
	proxy := http.ProxyURL(via)
	if via == nil {
		env := httpproxy.FromEnvironment().ProxyFunc()
		proxy = func(r *http.Request) (*url.URL, error) { return env(r.URL) }
	}

	return &http.Transport{
		Proxy:       proxy,
		DialContext: newDialer(conf).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !verify,
			RootCAs:            conf.TLSClientCAs,
			Certificates:       conf.TLSCertificates,
		},
		TLSHandshakeTimeout:   _tlsHandshakeTimeout,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          100,
		IdleConnTimeout:       _idleConnTimeout,
		DisableCompression:    true,
	}
}

// checkTargetURL parses target, the URL of a server to send requests on to, such as the target of a proxy reply or of
// a recording: an absolute http or https URL.
func checkTargetURL(target string) (*url.URL, error) {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("expected an absolute http or https URL, such as http://api.local:8080")
	}

	return u, nil
}

// parseVia parses the URL of a proxy to send through: an http, https, socks5 or socks5h URL, as http.Transport takes.
// An empty via is none.
func parseVia(via string) (*url.URL, error) {
	if via == "" {
		return nil, nil
	}

	u, err := url.Parse(via)
	if err != nil || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) || u.Host == "" {
		return nil, errors.New("expected an absolute http, https, socks5 or socks5h URL, such as http://proxy:3128")
	}

	return u, nil
}
