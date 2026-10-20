package mockhttp

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

// _idleConnTimeout closes a connection to an upstream that stays idle. A request still being served when its mock
// leaves returns its connection afterwards: it closes then, at the latest.
const _idleConnTimeout = 15 * time.Second

// newTransport returns a transport the server makes for its upstreams: the targets of proxy replies and of the
// recording, and the hosts of the forward proxy. It dials and shakes hands within the limits of http.DefaultTransport,
// and goes through via, when set, or else through the proxy of the environment (HTTP_PROXY, HTTPS_PROXY and NO_PROXY),
// which leaves out localhost. It passes compressed bodies on as they come, and speaks HTTP/1.1 to the upstreams.
// verify sets whether it verifies the TLS certificates of the upstreams, with the server's client CAs.
func newTransport(conf *Config, verify bool, via *url.URL) *http.Transport {
	proxy := http.ProxyFromEnvironment
	if via != nil {
		proxy = http.ProxyURL(via)
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

	return &http.Transport{
		Proxy:       proxy,
		DialContext: dialer.DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !verify,
			RootCAs:            conf.TLSClientCAs,
			Certificates:       conf.TLSCertificates,
		},
		TLSHandshakeTimeout:   10 * time.Second,
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

// parseVia parses the URL of a proxy to send through, an absolute http or https URL. An empty via is none.
func parseVia(via string) (*url.URL, error) {
	if via == "" {
		return nil, nil
	}

	u, err := url.Parse(via)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("expected an absolute http or https URL, such as http://proxy:3128")
	}

	return u, nil
}
