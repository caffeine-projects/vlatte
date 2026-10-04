package test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/caffeine-projects/vlatte/flavor/xhttp"
)

func TestHTTP2(t *testing.T) {
	cert, err := tls.LoadX509KeyPair(_tlsCertFile, _tlsKeyFile)
	require.NoError(t, err)

	caCert, err := os.ReadFile(_tlsClientCertFile)
	require.NoError(t, err)

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:      caCertPool,
			Certificates: []tls.Certificate{cert},
		},
		Protocols: new(http.Protocols),
	}
	tr.Protocols.SetHTTP2(true)
	t.Cleanup(tr.CloseIdleConnections)
	client := &http.Client{Transport: tr}

	m := New(Setup().TLSMutual(_tlsCertFile, _tlsKeyFile, _tlsClientCertFile))
	t.Cleanup(func() { m.Close() })
	m.MustStartTLS()
	m.MustMock(Getf("/test").Reply(OK().PlainText("hello world")))

	res, err := client.Get(m.URL("/test"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello world", string(b))
	require.Equal(t, "HTTP/2.0", res.Proto)
}
