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
