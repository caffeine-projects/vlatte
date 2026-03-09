package test

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/caffeine-projects/vlatte/drink/xhttp"
	. "github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/sip"
)

func TestProxy(t *testing.T) {
	proxySrv := New(Setup().Proxy())
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStart()
	proxyScope := proxySrv.MustBrew(Get(URLPath("/test")).Reply(Accepted()))

	targetSrv := New()
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStart()
	targetScope := targetSrv.MustBrew(Get(URLPath("/other")).Reply(Created()))

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
	proxySrv := New(Setup().Proxy(&ProxyConfig{SSLVerify: false}))
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStartTLS()
	proxyScope := proxySrv.MustBrew(Get(URLPath("/test")).Reply(Accepted()))

	targetSrv := New()
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStart()
	targetScope := targetSrv.MustBrew(Get(URLPath("/other")).Reply(Created()))

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

func TestProxyTLS_CustomCert(t *testing.T) {
	caCert, err := os.ReadFile(filepath.Clean(_tlsCertFile))
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caCert)

	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	transport := &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}

	proxySrv := New(
		Setup().
			Proxy(&ProxyConfig{SSLVerify: true, Transport: transport}).TLSCertKeyPair(_tlsCertFile, _tlsKeyFile))
	t.Cleanup(func() { proxySrv.Close() })
	proxySrv.MustStartTLS()
	proxyScope := proxySrv.MustBrew(Get(URLPath("/test")).Reply(Accepted()))

	targetSrv := New(Setup().TLSCertKeyPair(_tlsCertFile, _tlsKeyFile))
	t.Cleanup(func() { targetSrv.Close() })
	targetSrv.MustStart()
	targetScope := targetSrv.MustBrew(Get(URLPath("/other")).Reply(Created()))

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
	p := New(Setup().Proxy())
	t.Cleanup(func() { p.Close() })
	p.MustStart()
	scope1 := p.MustBrew(Get(URLPath("/test")).Reply(Accepted()))

	v := New(Setup().Proxy(&ProxyConfig{Via: p.URL()}))
	t.Cleanup(func() { v.Close() })
	v.MustStart()
	scope2 := v.MustBrew(Get(URLPath("/unknown")).Reply(NoContent()))

	m := New()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustBrew(Get(URLPath("/other")).Reply(Created()))

	u, _ := url.Parse(v.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}

	res, err := client.Get(m.URL() + "/test")

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	scope1.AssertCalled(t)
	sip.NotCalled(t, scope2)
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	res, err = client.Get(m.URL() + "/other")

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)
}
