package jose

import (
	"crypto"
	"encoding/base64"
	json "encoding/json/v2"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeySet_VerifiesTheRFC7520Examples(t *testing.T) {
	// Signatures another implementation made, so the encoding of each family, ECDSA's R and S above all, is the one
	// clients read, not just the one this package writes.
	b, err := os.ReadFile("testdata/rfc7520.json")
	require.NoError(t, err)

	var examples []struct {
		Name    string         `json:"name"`
		Key     map[string]any `json:"key"`
		Compact string         `json:"compact"`
	}
	require.NoError(t, json.Unmarshal(b, &examples))
	require.Len(t, examples, 3)

	for _, ex := range examples {
		t.Run(ex.Name, func(t *testing.T) {
			ks, err := ParseKeySet(mustJSON(t, ex.Key))
			require.NoError(t, err)

			// The payloads of RFC 7520 are text, not claims, so Parse refuses them: verify the signing input as it is.
			parts := strings.Split(ex.Compact, ".")
			require.Len(t, parts, 3)

			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			require.NoError(t, err)

			tok := &Token{input: []byte(parts[0] + "." + parts[1]), signature: sig}
			require.NoError(t, decodeJSON("header", parts[0], &tok.Header))
			tok.Alg, _ = tok.Header["alg"].(string)
			tok.Kid, _ = tok.Header["kid"].(string)

			require.NoError(t, ks.Verify(tok))

			tok.input = append(tok.input, '!')
			require.ErrorIs(t, ks.Verify(tok), ErrSignature)
		})
	}
}

func TestPublicJWK_RoundTripsEachKeyType(t *testing.T) {
	tests := []struct {
		alg    string
		kty    string
		crv    string
		fields int // of n, e, x and y, how many the JWK has
	}{
		{alg: "RS256", kty: "RSA", fields: 2},
		{alg: "ES256", kty: "EC", crv: "P-256", fields: 2},
		{alg: "ES384", kty: "EC", crv: "P-384", fields: 2},
		{alg: "ES512", kty: "EC", crv: "P-521", fields: 2},
		{alg: "EdDSA", kty: "OKP", crv: "Ed25519", fields: 1},
	}
	for _, tt := range tests {
		t.Run(tt.alg, func(t *testing.T) {
			key := testKey(t, tt.alg)

			jwk, err := PublicJWK(tt.alg, "key-1", key.Public())
			require.NoError(t, err)
			require.Equal(t, tt.kty, jwk.Kty)
			require.Equal(t, tt.crv, jwk.Crv)
			require.Equal(t, "sig", jwk.Use)
			require.Equal(t, tt.alg, jwk.Alg)
			require.Equal(t, "key-1", jwk.Kid)

			present := 0
			for _, f := range []string{jwk.N, jwk.E, jwk.X, jwk.Y} {
				if f != "" {
					present++
				}
			}
			require.Equal(t, tt.fields, present)

			pub, err := jwk.publicKey()
			require.NoError(t, err)
			require.True(t, key.Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(pub))
		})
	}
}

func TestPublicJWK_WritesTheExponentAsRFC7518Does(t *testing.T) {
	jwk, err := PublicJWK("RS256", "", testKey(t, "RS256").Public())
	require.NoError(t, err)
	require.Equal(t, "AQAB", jwk.E) // 65537, big-endian, no leading zero
}

func TestParseKeySet_ReadsSetsSingleKeysAndPEM(t *testing.T) {
	rsaJWK, err := PublicJWK("RS256", "key-1", testKey(t, "RS256").Public())
	require.NoError(t, err)

	pemKey := pemBlock("PUBLIC KEY", mustPKIX(t, testKey(t, "ES256").Public()))

	tests := []struct {
		name string
		text []byte
		keys int
	}{
		{name: "a JWK Set", text: mustJSON(t, JWKSet{Keys: []JWK{rsaJWK}}), keys: 1},
		{name: "a single JWK", text: mustJSON(t, rsaJWK), keys: 1},
		{name: "a set whose encryption keys are skipped", text: mustJSON(t, JWKSet{Keys: []JWK{rsaJWK, {Kty: "RSA", Use: "enc", N: rsaJWK.N, E: rsaJWK.E}}}), keys: 1},
		{name: "PEM public keys", text: append(pemKey, pemBlock("RSA PUBLIC KEY", mustPKCS1(t, testKey(t, "RS256")))...), keys: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks, err := ParseKeySet(tt.text)
			require.NoError(t, err)
			require.Len(t, ks.keys, tt.keys)
		})
	}
}

func TestParseKeySet_RefusesWhatHoldsNoPublicKey(t *testing.T) {
	tests := []struct {
		name string
		text string
		msg  string
	}{
		{name: "neither JSON nor PEM", text: "key-1", msg: "jose: a key set is a JWK Set, a JWK or PEM public keys"},
		{name: "an empty set", text: `{"keys":[]}`, msg: "jose: the key set has no signing key"},
		{name: "an unknown kty", text: `{"keys":[{"kty":"oct","k":"c2VjcmV0"}]}`, msg: `jose: jwk: unsupported kty "oct"`},
		{name: "an unknown curve", text: `{"kty":"EC","crv":"secp256k1","x":"AA","y":"AA"}`, msg: `jose: jwk: unsupported curve "secp256k1"`},
		{name: "an RSA key without n", text: `{"kty":"RSA","e":"AQAB"}`, msg: "jose: jwk: no n"},
		{
			// A private key pasted where the public one goes would be served by the admin API with the mock.
			name: "a private key", text: string(pemBlock("PRIVATE KEY", []byte("x"))),
			msg: `jose: PEM block "PRIVATE KEY" is not a public key or a certificate`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseKeySet([]byte(tt.text))
			require.EqualError(t, err, tt.msg)
		})
	}
}

func TestKeySet_Verify(t *testing.T) {
	rsaKey := testKey(t, "RS256")
	ecKey := testKey(t, "ES256")

	sign := func(alg, kid string) *Token {
		key := rsaKey
		if alg == "ES256" {
			key = ecKey
		}

		token, err := Sign(alg, kid, key, []byte(`{"sub":"ana"}`))
		require.NoError(t, err)

		tok, err := Parse(token)
		require.NoError(t, err)

		return tok
	}

	rs256, err := PublicJWK("RS256", "key-1", rsaKey.Public())
	require.NoError(t, err)

	es256, err := PublicJWK("ES256", "key-2", ecKey.Public())
	require.NoError(t, err)

	set := func(keys ...JWK) *KeySet {
		ks, err := ParseKeySet(mustJSON(t, JWKSet{Keys: keys}))
		require.NoError(t, err)

		return ks
	}

	pem, err := ParseKeySet(pemBlock("PUBLIC KEY", mustPKIX(t, rsaKey.Public())))
	require.NoError(t, err)

	t.Run("finds the key by kid", func(t *testing.T) {
		require.NoError(t, set(rs256, es256).Verify(sign("ES256", "key-2")))
	})

	t.Run("tries every key of a token without kid", func(t *testing.T) {
		require.NoError(t, set(rs256, es256).Verify(sign("ES256", "")))
	})

	t.Run("tries keys without kid, such as PEM ones", func(t *testing.T) {
		require.NoError(t, pem.Verify(sign("RS256", "anything")))
	})

	t.Run("refuses a kid no key has", func(t *testing.T) {
		err := set(rs256).Verify(sign("RS256", "key-9"))
		require.ErrorIs(t, err, ErrUnknownKid)
		require.EqualError(t, err, `jose: kid not in the key set: "key-9"`)
	})

	t.Run("refuses a key another key signed with the same kid", func(t *testing.T) {
		other, err := PublicJWK("RS256", "key-1", testKeyOther(t).Public())
		require.NoError(t, err)
		require.ErrorIs(t, set(other).Verify(sign("RS256", "key-1")), ErrSignature)
	})

	t.Run("reads a key only as the alg its JWK names", func(t *testing.T) {
		// A token can't make the verifier read an RS256 key as PS256.
		require.ErrorIs(t, set(rs256).Verify(sign("PS256", "key-1")), ErrSignature)
	})

	t.Run("refuses none and HMAC", func(t *testing.T) {
		for _, alg := range []string{"none", "HS256"} {
			tok := sign("RS256", "key-1")
			tok.Alg = alg
			require.ErrorIs(t, set(rs256).Verify(tok), ErrUnsupportedAlg, alg)
		}
	})
}

// Generators

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	require.NoError(t, err)

	return b
}
