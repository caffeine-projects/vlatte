package test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	. "github.com/caffeine-projects/vlatte/filter"
	. "github.com/caffeine-projects/vlatte/flavor/xhttp"
)

const (
	_tlsCertFile       = "testdata/cert/cert.pem"
	_tlsKeyFile        = "testdata/cert/key.pem"
	_tlsClientCertFile = "testdata/cert/cert_client.pem"
	_tlsClientKeyFile  = "testdata/cert/key_client.pem"
)

func TestTLS(t *testing.T) {
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}

	testCases := []struct {
		name   string
		config vlatte.Configurer[*Config]
	}{
		{"default", Setup()},
		{"custom key/pair", Setup().TLSCertKeyPair(_tlsCertFile, _tlsKeyFile)},
		{"custom tls config", Setup().TLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}})},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := MustNew(tc.config)
			t.Cleanup(func() { m.Close() })
			m.MustStartTLS()

			scoped := m.MustMock(
				Get(URLPath("/test")).
					Header("test", StrictEqual("hello")).
					Reply(OK().PlainText("hello+world")))

			req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
			req.Header.Add("test", "hello")

			res, err := client.Do(req)
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, "hello+world", string(b))
			require.True(t, scoped.Called())
		})
	}
}

func TestTLS_FileSetup(t *testing.T) {
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	m := MustNew(UseConfig("testdata/tls/tls_app.yaml"))
	t.Cleanup(func() { m.Close() })
	m.MustStartTLS()

	scoped := m.MustMock(
		Get(URLPath("/test")).
			Header("test", StrictEqual("hello")).
			Reply(OK().PlainText("hello+world")))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	req.Header.Add("test", "hello")

	res, err := client.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "hello+world", string(b))
	require.True(t, scoped.Called())
}

func TestTLSMutual(t *testing.T) {
	cert, err := tls.LoadX509KeyPair(_tlsClientCertFile, _tlsClientKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      certPool,
				ClientAuth:   tls.RequireAndVerifyClientCert,
			},
		},
	}

	target := MustNew(Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	t.Cleanup(func() { target.Close() })
	target.MustStartTLS()
	target.MustMock(Getf("/test").Reply(Accepted().PlainText("accepted")))

	req, _ := http.NewRequest(http.MethodGet, target.URL("/test"), nil)
	res, err := client.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	require.Equal(t, "accepted", string(b))

	// Mutual TLS: a client that trusts the server but has no certificate must be refused.
	noCertClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: certPool}},
	}
	res, err = noCertClient.Get(target.URL("/test"))
	if err == nil {
		_ = res.Body.Close()
	}
	require.Error(t, err)

	// Only certificates the client CAs trust are accepted: the server's own certificate is not one of them.
	serverCert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)
	wrongCertClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{serverCert}, RootCAs: certPool}},
	}
	res, err = wrongCertClient.Get(target.URL("/test"))
	if err == nil {
		_ = res.Body.Close()
	}
	require.Error(t, err)
}

func TestTLSMutualWithProxy(t *testing.T) {
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      certPool,
			},
		},
	}

	// The forward reply connects with the server's certificate and verifies the target with the client CAs,
	// so every hop shares one identity.
	target := MustNew(Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsCertFile))
	t.Cleanup(func() { target.Close() })
	target.MustStartTLS()
	target.MustMock(Getf("/test").Reply(Accepted().PlainText("accepted")))

	m := MustNew(Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsCertFile))
	t.Cleanup(func() { m.Close() })
	m.MustStartTLS()
	m.MustMock(Get(URLPath("/test")).
		Header("test", StrictEqual("hello")).
		Reply(From(target.URL()).SSLVerify(true)))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	req.Header.Add("test", "hello")

	res, err := client.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusAccepted, res.StatusCode)
	require.Equal(t, "accepted", string(b))
}

func TestTLSMutualWithProxy_FileSetup(t *testing.T) {
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(filepath.Clean(_tlsCertFile))
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      certPool,
			},
		},
	}

	// Both servers share one identity, as in TestTLSMutualWithProxy.
	target := MustNew(UseConfig("testdata/tls/tls_ca_target.yaml"))
	t.Cleanup(func() { target.Close() })
	target.MustStartTLS()
	target.MustMock(Getf("/test").Reply(Accepted().PlainText("accepted")))

	m := MustNew(UseConfig("testdata/tls/tls_ca.yaml"))
	t.Cleanup(func() { m.Close() })
	m.MustStartTLS()
	m.MustMock(Get(URLPath("/test")).
		Header("test", StrictEqual("hello")).
		Reply(From(target.URL()).SSLVerify(true)))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)
	req.Header.Add("test", "hello")

	res, err := client.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, "accepted", string(b))
	require.Equal(t, http.StatusAccepted, res.StatusCode)
}
