package match

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestSignedJWT_MatchesOnlyTokensSignedByTheKeys(t *testing.T) {
	trusted := newECKey(t)
	other := newECKey(t)
	keys := jwksOf(t, map[string]*ecdsa.PrivateKey{"key-1": trusted})

	hour := time.Now().Add(time.Hour).Unix()
	ago := time.Now().Add(-time.Hour).Unix()
	b64 := base64.RawURLEncoding.EncodeToString

	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{name: "signed by the key", token: signJWT(t, trusted, "key-1", fmt.Sprintf(`{"sub":"ana","exp":%d}`, hour)), want: true},
		{name: "without exp", token: signJWT(t, trusted, "key-1", `{"sub":"ana"}`), want: true},
		{name: "signed by another key with the kid", token: signJWT(t, other, "key-1", `{"sub":"ana"}`), want: false},
		{name: "of a kid the keys lack", token: signJWT(t, trusted, "key-2", `{"sub":"ana"}`), want: false},
		{name: "expired", token: signJWT(t, trusted, "key-1", fmt.Sprintf(`{"sub":"ana","exp":%d}`, ago)), want: false},
		{name: "not yet valid", token: signJWT(t, trusted, "key-1", fmt.Sprintf(`{"sub":"ana","nbf":%d}`, hour)), want: false},
		{name: "unsigned", token: b64([]byte(`{"alg":"none"}`)) + "." + b64([]byte(`{"sub":"ana"}`)) + ".", want: false},
		{
			// HS256 signed with the public key as the secret: the confusion attack HMAC allows.
			name: "HS256", token: b64([]byte(`{"alg":"HS256","kid":"key-1"}`)) + "." + b64([]byte(`{"sub":"ana"}`)) + ".c2ln", want: false,
		},
		{name: "claims that don't match", token: signJWT(t, trusted, "key-1", `{"sub":"bob"}`), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := SignedJWT(keys, JSONPath("$.claims.sub", Equal("ana")))

			ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "Bearer "+tt.token))
			require.NoError(t, err)
			require.Equal(t, tt.want, ok)
		})
	}
}

func TestSignedJWT_TakesPEMPublicKeys(t *testing.T) {
	// What an app registers with a provider, such as the key of a GitHub App, is often a PEM key, without kid.
	key := newECKey(t)

	der, err := x509.MarshalPKIXPublicKey(key.Public())
	require.NoError(t, err)

	m := SignedJWT(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), Anything())

	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), nil, signJWT(t, key, "any", `{"iss":"123"}`)))
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = m.Match(vlatte.NewMatcherArg(t.Context(), nil, signJWT(t, newECKey(t), "any", `{"iss":"123"}`)))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSignedJWT_PanicsOnKeysItCantTrust(t *testing.T) {
	der, err := x509.MarshalPKCS8PrivateKey(newECKey(t))
	require.NoError(t, err)

	tests := []struct {
		name string
		keys string
		msg  string
	}{
		{name: "no key", keys: `{"keys":[]}`, msg: "match: signedjwt: jose: the key set has no signing key"},
		{name: "not keys", keys: "key-1", msg: "match: signedjwt: jose: a key set is a JWK Set, a JWK or PEM public keys"},
		{
			name: "a private key", keys: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
			msg: `match: signedjwt: jose: PEM block "PRIVATE KEY" is not a public key or a certificate`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.PanicsWithError(t, tt.msg, func() { SignedJWT(tt.keys, Anything()) })
		})
	}
}

func TestSignedJWT_Describes(t *testing.T) {
	keys := jwksOf(t, map[string]*ecdsa.PrivateKey{"key-1": newECKey(t)})

	data, err := SignedJWT(keys, Anything()).(vlatte.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, fmt.Sprintf(`["signedJWT", %q, ["anything"]]`, keys), string(data))
}
