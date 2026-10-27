package e2e

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestProxy(t *testing.T) {
	proxySrv := mockhttp.MustNew(mockhttp.Setup().Proxy())
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStart()
	proxyScope := proxySrv.MustMock(mockhttp.Get(match.URLPath("/test")).Reply(mockhttp.Accepted()))

	targetSrv := mockhttp.MustNew()
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStart()
	targetScope := targetSrv.MustMock(mockhttp.Get(match.URLPath("/other")).Reply(mockhttp.Created()))

	// client that acts like a browser proxying requests to our server
	proxyURL, _ := url.Parse(proxySrv.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	res, err := client.Get(targetSrv.URL() + "/test")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = client.Get(targetSrv.URL() + "/other")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	proxyScope.AssertCalled(t)
	targetScope.AssertCalled(t)
}

func TestProxyTLS(t *testing.T) {
	proxySrv := mockhttp.MustNew(mockhttp.Setup().Proxy(&mockhttp.ProxyConfig{SSLVerify: false}))
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStartTLS()
	proxyScope := proxySrv.MustMock(mockhttp.Get(match.URLPath("/test")).Reply(mockhttp.Accepted()))

	targetSrv := mockhttp.MustNew()
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStart()
	targetScope := targetSrv.MustMock(mockhttp.Get(match.URLPath("/other")).Reply(mockhttp.Created()))

	// client that acts like a browser proxying requests to our server
	proxyURL, _ := url.Parse(proxySrv.URL())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	res, err := client.Get(targetSrv.URL() + "/test")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = client.Get(targetSrv.URL() + "/other")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	proxyScope.AssertCalled(t)
	targetScope.AssertCalled(t)
}

func TestProxyTunnelsHTTPSTargets(t *testing.T) {
	proxySrv := mockhttp.MustNew(mockhttp.Setup().Proxy())
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStart()

	targetSrv := mockhttp.MustNew()
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStartTLS()
	targetScope := targetSrv.MustMock(mockhttp.Get(match.URLPath("/secure")).Reply(mockhttp.OK()))

	proxyURL, _ := url.Parse(proxySrv.URL())
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	t.Cleanup(client.CloseIdleConnections)

	// An https target makes the client open a CONNECT tunnel through the proxy.
	res, err := client.Get(targetSrv.URL() + "/secure")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	targetScope.AssertCalled(t)
}

func TestProxyTLS_CustomCert(t *testing.T) {
	caCert, err := os.ReadFile(filepath.Clean(_tlsCertFile))
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	transport := &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}

	proxySrv := mockhttp.MustNew(
		mockhttp.Setup().
			Proxy(&mockhttp.ProxyConfig{Transport: transport}).TLSCertKeyPair(_tlsCertFile, _tlsKeyFile))
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStartTLS()
	proxyScope := proxySrv.MustMock(mockhttp.Get(match.URLPath("/test")).Reply(mockhttp.Accepted()))

	targetSrv := mockhttp.MustNew(mockhttp.Setup().TLSCertKeyPair(_tlsCertFile, _tlsKeyFile))
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStart()
	targetScope := targetSrv.MustMock(mockhttp.Get(match.URLPath("/other")).Reply(mockhttp.Created()))

	// client that acts like a browser proxying requests to our server
	proxyURL, _ := url.Parse(proxySrv.URL())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}

	res, err := client.Get(targetSrv.URL() + "/test")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = client.Get(targetSrv.URL() + "/other")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	proxyScope.AssertCalled(t)
	targetScope.AssertCalled(t)
}

func TestProxyViaAnotherProxy(t *testing.T) {
	p := mockhttp.MustNew(mockhttp.Setup().Proxy())
	t.Cleanup(func() { p.Close() })
	p.MustStart()
	scope1 := p.MustMock(mockhttp.Get(match.URLPath("/test")).Reply(mockhttp.Accepted()))

	v := mockhttp.MustNew(mockhttp.Setup().Proxy(&mockhttp.ProxyConfig{Via: p.URL()}))
	t.Cleanup(func() { v.Close() })
	v.MustStart()
	scope2 := v.MustMock(mockhttp.Get(match.URLPath("/unknown")).Reply(mockhttp.NoContent()))

	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.Get(match.URLPath("/other")).Reply(mockhttp.Created()))

	u, _ := url.Parse(v.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}

	res, err := client.Get(m.URL() + "/test")

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	scope1.AssertCalled(t)
	vlatte.NotCalled(t, scope2)
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = client.Get(m.URL() + "/other")

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestProxy_MocksMatchTheURLTheClientAskedFor(t *testing.T) {
	// The URL mocks matched was the server's, for proxied requests too: scheme: https never matched an intercepted
	// request, nor an absolute url the host of a proxied one. They match the request's own scheme and host now.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "target")
	}))
	t.Cleanup(target.Close)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Proxy(&mockhttp.ProxyConfig{Intercept: true}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		mockhttp.Request().Scheme("https").URL(match.Equal(target.URL+"/secure")).Reply(mockhttp.OK().PlainText("intercepted")),
		mockhttp.Request().Scheme("http").URL(match.Equal("http://api.example.com/users")).Reply(mockhttp.OK().PlainText("proxied")))

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(m.ProxyCA())

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: pool}}
	t.Cleanup(transport.CloseIdleConnections)

	client := &http.Client{Transport: transport}
	body := func(target string) string {
		res, err := client.Get(target)
		require.NoError(t, err, target)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return string(b)
	}

	require.Equal(t, "intercepted", body(target.URL+"/secure"))
	require.Equal(t, "proxied", body("http://api.example.com/users"))
}
