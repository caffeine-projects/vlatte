package jose

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSign_EachAlgorithmSignsATokenItsPublicKeyVerifies(t *testing.T) {
	for _, alg := range Algorithms() {
		t.Run(alg, func(t *testing.T) {
			key := testKey(t, alg)

			token, err := Sign(alg, "key-1", key, []byte(`{"sub":"ana"}`))
			require.NoError(t, err)

			tok, err := Parse(token)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"alg": alg, "kid": "key-1", "typ": "JWT"}, tok.Header)
			require.Equal(t, map[string]any{"sub": "ana"}, tok.Claims)

			require.NoError(t, keySetOf(t, alg, "key-1", key).Verify(tok))
		})
	}
}

func TestSign_LeavesOutAnEmptyKid(t *testing.T) {
	token, err := Sign("ES256", "", testKey(t, "ES256"), []byte(`{}`))
	require.NoError(t, err)

	tok, err := Parse(token)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"alg": "ES256", "typ": "JWT"}, tok.Header)
}

func TestSign_RS256SignsTheSamePayloadTheSameWay(t *testing.T) {
	// A mock that renders the same claims answers the same token, so a test can compare it.
	key := testKey(t, "RS256")

	a, err := Sign("RS256", "k", key, []byte(`{"sub":"ana"}`))
	require.NoError(t, err)

	b, err := Sign("RS256", "k", key, []byte(`{"sub":"ana"}`))
	require.NoError(t, err)
	require.Equal(t, a, b)
}

func TestCheckKey_RefusesWhatTheAlgorithmDoesntTake(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)

	tests := []struct {
		name string
		alg  string
		key  crypto.Signer
		is   error
		msg  string
	}{
		{
			name: "HMAC, whose secret no provider publishes",
			alg:  "HS256",
			key:  testKey(t, "RS256"),
			is:   ErrUnsupportedAlg,
			msg:  `jose: unsupported algorithm "HS256": HMAC signs with a shared secret no provider publishes: use one of ES256, ES384, ES512, EdDSA, PS256, PS384, PS512, RS256, RS384, RS512`,
		},
		{
			name: "none",
			alg:  "none",
			key:  testKey(t, "RS256"),
			is:   ErrUnsupportedAlg,
			msg:  `jose: unsupported algorithm "none": an unsigned token proves nothing`,
		},
		{
			name: "an unknown algorithm",
			alg:  "RS1",
			key:  testKey(t, "RS256"),
			is:   ErrUnsupportedAlg,
			msg:  `jose: unsupported algorithm "RS1": use one of ES256, ES384, ES512, EdDSA, PS256, PS384, PS512, RS256, RS384, RS512`,
		},
		{
			name: "an ECDSA key for RS256",
			alg:  "RS256",
			key:  testKey(t, "ES256"),
			is:   ErrKeyMismatch,
			msg:  "jose: key does not fit the algorithm: RS256 takes an RSA key, not an ECDSA key",
		},
		{
			name: "an RSA key under 2048 bits",
			alg:  "PS256",
			key:  small,
			is:   ErrKeyMismatch,
			msg:  "jose: key does not fit the algorithm: PS256 takes an RSA key of 2048 bits or more, not 1024",
		},
		{
			name: "a key on another curve",
			alg:  "ES256",
			key:  p384,
			is:   ErrKeyMismatch,
			msg:  "jose: key does not fit the algorithm: ES256 takes a key on P-256, not P-384",
		},
		{
			name: "an RSA key for EdDSA",
			alg:  "EdDSA",
			key:  testKey(t, "RS256"),
			is:   ErrKeyMismatch,
			msg:  "jose: key does not fit the algorithm: EdDSA takes an Ed25519 key, not an RSA key",
		},
		{
			name: "no key",
			alg:  "RS256",
			is:   ErrKeyMismatch,
			msg:  "jose: key does not fit the algorithm: no key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckKey(tt.alg, tt.key)
			require.ErrorIs(t, err, tt.is)
			require.EqualError(t, err, tt.msg)

			_, err = Sign(tt.alg, "k", tt.key, []byte(`{}`))
			require.ErrorIs(t, err, tt.is)
		})
	}
}

func TestGenerateKey_MakesAKeyForTheAlgorithm(t *testing.T) {
	for _, alg := range []string{"ES256", "ES384", "ES512", "EdDSA"} {
		key, err := GenerateKey(alg)
		require.NoError(t, err)
		require.NoError(t, CheckKey(alg, key), alg)
	}

	_, err := GenerateKey("HS256")
	require.ErrorIs(t, err, ErrUnsupportedAlg)
}

func TestParsePrivateKey_ReadsTheCommonPEMForms(t *testing.T) {
	rsaKey := testKey(t, "RS256").(*rsa.PrivateKey)
	ecKey := testKey(t, "ES256").(*ecdsa.PrivateKey)
	edKey := testKey(t, "EdDSA").(ed25519.PrivateKey)

	pkcs8 := func(key any) []byte {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, err)

		return pemBlock("PRIVATE KEY", der)
	}

	sec1, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)

	tests := []struct {
		name string
		pem  []byte
		want crypto.Signer
	}{
		{name: "PKCS #1 RSA", pem: pemBlock("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey)), want: rsaKey},
		{name: "PKCS #8 RSA", pem: pkcs8(rsaKey), want: rsaKey},
		{name: "PKCS #8 ECDSA", pem: pkcs8(ecKey), want: ecKey},
		{name: "PKCS #8 Ed25519", pem: pkcs8(edKey), want: edKey},
		{name: "SEC 1 ECDSA", pem: pemBlock("EC PRIVATE KEY", sec1), want: ecKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := ParsePrivateKey(tt.pem)
			require.NoError(t, err)
			require.True(t, tt.want.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(key.Public()))
		})
	}
}

func TestParsePrivateKey_ErrorsHoldNoKeyMaterial(t *testing.T) {
	// A key file that fails to load must not print its content into the server's logs.
	secret := []byte("not-a-key-but-a-secret")

	tests := []struct {
		name string
		pem  []byte
		msg  string
	}{
		{name: "a broken key", pem: pemBlock("PRIVATE KEY", secret), msg: `jose: PEM block "PRIVATE KEY": `},
		{name: "a public key", pem: pemBlock("PUBLIC KEY", secret), msg: `jose: PEM block "PUBLIC KEY" is not a private key`},
		{name: "no PEM", pem: secret, msg: "jose: no PEM block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePrivateKey(tt.pem)
			require.ErrorContains(t, err, tt.msg)
			require.NotContains(t, err.Error(), string(secret))
		})
	}
}

// Generators

// _testKeys are keys for each algorithm, made once: RSA keys are slow to make, more so with the race detector.
var _testKeys = sync.OnceValue(func() map[string]crypto.Signer {
	keys := make(map[string]crypto.Signer)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	for _, alg := range Algorithms() {
		if strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS") {
			keys[alg] = rsaKey
			continue
		}

		key, err := GenerateKey(alg)
		if err != nil {
			panic(err)
		}

		keys[alg] = key
	}

	return keys
})

func testKey(t *testing.T, alg string) crypto.Signer {
	t.Helper()

	key, ok := _testKeys()[alg]
	require.True(t, ok, alg)

	return key
}

// keySetOf returns the key set the JWK Set of key, as kid, reads as.
func keySetOf(t *testing.T, alg, kid string, key crypto.Signer) *KeySet {
	t.Helper()

	jwk, err := PublicJWK(alg, kid, key.Public())
	require.NoError(t, err)

	ks, err := ParseKeySet(mustJSON(t, JWKSet{Keys: []JWK{jwk}}))
	require.NoError(t, err)

	return ks
}

// _otherKey is an RSA key that isn't the one of _testKeys.
var _otherKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	return key
})

func testKeyOther(t *testing.T) crypto.Signer {
	t.Helper()

	return _otherKey()
}

func mustPKIX(t *testing.T, pub crypto.PublicKey) []byte {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)

	return der
}

func mustPKCS1(t *testing.T, key crypto.Signer) []byte {
	t.Helper()

	return x509.MarshalPKCS1PublicKey(key.Public().(*rsa.PublicKey))
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}
