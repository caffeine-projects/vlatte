package match

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	json "encoding/json/v2"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/jose"
)

func TestJWT_MatchesTheHeaderAndClaimsOfAToken(t *testing.T) {
	token := signJWT(t, newECKey(t), "key-1", `{"sub":"ana","aud":["my-app"]}`)

	tests := []struct {
		name    string
		value   any
		matcher vlatte.Matcher
		want    bool
	}{
		{name: "a claim", value: token, matcher: JSONPath("$.claims.sub", Equal("ana")), want: true},
		{name: "a claim that differs", value: token, matcher: JSONPath("$.claims.sub", Equal("bob")), want: false},
		{name: "a list claim", value: token, matcher: JSONPath("$.claims.aud", Contain("my-app")), want: true},
		{name: "the header", value: token, matcher: JSONPath("$.header.kid", Equal("key-1")), want: true},
		{name: "after a Bearer scheme", value: "Bearer " + token, matcher: JSONPath("$.claims.sub", Equal("ana")), want: true},
		{name: "after a scheme of any case", value: "bearer  " + token, matcher: JSONPath("$.claims.sub", Equal("ana")), want: true},
		{name: "as bytes", value: []byte(token), matcher: Anything(), want: true},
		{name: "text that isn't a JWT", value: "Bearer gho_opaque", matcher: Anything(), want: false},
		{name: "a value that isn't text", value: 42, matcher: Anything(), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := JWT(tt.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tt.value))
			require.NoError(t, err)
			require.Equal(t, tt.want, ok)
		})
	}
}

func TestJWT_ReturnsTheErrorOfItsMatcher(t *testing.T) {
	// A matcher that fails on the claims is an error, not a token that didn't match.
	boom := errors.New("boom")
	token := signJWT(t, newECKey(t), "key-1", `{"sub":"ana"}`)

	ok, err := JWT(Function(func(any) (bool, error) { return false, boom })).
		Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), token))
	require.ErrorIs(t, err, boom)
	require.False(t, ok)
}

func TestJWT_DoesNotVerifyTheSignature(t *testing.T) {
	// JWT reads any token, signed by anyone: what SignedJWT is for.
	token := signJWT(t, newECKey(t), "key-1", `{"sub":"ana"}`)

	ok, err := JWT(Anything()).Match(vlatte.NewMatcherArg(t.Context(), nil, token[:len(token)-4]+"AAAA"))
	require.NoError(t, err)
	require.True(t, ok)
}

func TestJWT_Describes(t *testing.T) {
	data, err := JWT(JSONPath("$.claims.sub", Equal("ana"))).(vlatte.SelfDescriptive).Describe().MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["jwt", ["jsonPath", "$.claims.sub", ["eq", "ana"]]]`, string(data))
}

// Generators

func newECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	return key
}

// signJWT returns claims signed with key, an ES256 one, as kid.
func signJWT(t *testing.T, key crypto.Signer, kid, claims string) string {
	t.Helper()

	token, err := jose.Sign("ES256", kid, key, []byte(claims))
	require.NoError(t, err)

	return token
}

// jwksOf returns the JWK Set of the public halves of keys, by kid, as JSON text.
func jwksOf(t *testing.T, keys map[string]*ecdsa.PrivateKey) string {
	t.Helper()

	var set jose.JWKSet

	for kid, key := range keys {
		jwk, err := jose.PublicJWK("ES256", kid, key.Public())
		require.NoError(t, err)

		set.Keys = append(set.Keys, jwk)
	}

	data, err := json.Marshal(set)
	require.NoError(t, err)

	return string(data)
}
