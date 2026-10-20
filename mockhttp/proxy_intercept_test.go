package mockhttp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// signedCA returns a CA signed by parent, or self-signed when parent is nil, with usage as its key usages: 0 leaves
// them out, as openssl does by default.
func signedCA(t *testing.T, name string, usage x509.KeyUsage, parent *tls.Certificate) *tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              usage,
	}

	issuer, issuerKey := template, any(key)
	if parent != nil {
		issuer, issuerKey = parent.Leaf, parent.PrivateKey
	}

	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	chain := [][]byte{der}
	if parent != nil {
		chain = append(chain, parent.Certificate...)
	}

	return &tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: cert}
}

func TestInterceptor_TakesACAWithoutKeyUsages(t *testing.T) {
	// openssl makes a CA without key usages by default, which x509 lets sign: such a CA was refused.
	in, err := newInterceptor(signedCA(t, "openssl CA", 0, nil))
	require.NoError(t, err)

	_, err = in.certificate("api.local")
	require.NoError(t, err)
}

func TestInterceptor_SendsTheChainOfAnIntermediateCA(t *testing.T) {
	// An intermediate CA comes with its chain, which clients that trust only the root need: it was left out.
	root := signedCA(t, "root", x509.KeyUsageCertSign, nil)
	intermediate := signedCA(t, "intermediate", x509.KeyUsageCertSign, root)

	in, err := newInterceptor(intermediate)
	require.NoError(t, err)

	cert, err := in.certificate("api.local")
	require.NoError(t, err)

	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root.Leaf)

	for _, der := range cert.Certificate[1:] {
		c, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		intermediates.AddCert(c)
	}

	_, err = cert.Leaf.Verify(x509.VerifyOptions{DNSName: "api.local", Roots: roots, Intermediates: intermediates})
	require.NoError(t, err)
}

func TestInterceptor_RefusesACAItCantSignWith(t *testing.T) {
	// A CA that can't sign the hosts' certificates fails New, saying why, rather than every handshake later. One given
	// without its parsed Leaf, as tls.Certificate may come, is parsed.
	ca := signedCA(t, "caller CA", x509.KeyUsageCertSign, nil)

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tests := []struct {
		name string
		ca   *tls.Certificate
		want string
	}{
		{name: "no certificate", ca: &tls.Certificate{}, want: "proxy: the CA has no certificate"},
		{
			name: "a certificate that doesn't parse",
			ca:   &tls.Certificate{Certificate: [][]byte{[]byte("not DER")}, PrivateKey: ca.PrivateKey},
			want: "proxy: the CA's certificate: ",
		},
		{
			name: "a key that can't sign",
			ca:   &tls.Certificate{Certificate: ca.Certificate, PrivateKey: "not a key", Leaf: ca.Leaf},
			want: "proxy: the CA's private key can't sign",
		},
		{
			name: "another certificate's key",
			ca:   &tls.Certificate{Certificate: ca.Certificate, PrivateKey: other, Leaf: ca.Leaf},
			want: `proxy: the CA's private key isn't the key of its certificate, of "caller CA"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, CA: tt.ca}))
			require.ErrorContains(t, err, tt.want)
		})
	}

	t.Run("no Leaf", func(t *testing.T) {
		m, err := New(Setup().Silent().Proxy(&ProxyConfig{Intercept: true, CA: &tls.Certificate{Certificate: ca.Certificate, PrivateKey: ca.PrivateKey}}))
		require.NoError(t, err)
		t.Cleanup(m.Close)
		require.True(t, ca.Leaf.Equal(m.ProxyCA()))
	})
}
