package jose

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse_RefusesWhatIsNotAJWT(t *testing.T) {
	b64 := base64.RawURLEncoding.EncodeToString

	tests := []struct {
		name  string
		token string
		msg   string
	}{
		{name: "two parts", token: b64([]byte(`{"alg":"RS256"}`)) + "." + b64([]byte(`{}`)), msg: "jose: not a JWT: a compact JWS has 3 parts"},
		{name: "five parts, a JWE", token: "a.b.c.d.e", msg: "jose: not a JWT: a compact JWS has 3 parts"},
		{name: "a header that isn't base64url", token: "%%%." + b64([]byte(`{}`)) + ".", msg: "jose: not a JWT: header: illegal base64 data at input byte 0"},
		{name: "a header that isn't an object", token: b64([]byte(`[1]`)) + "." + b64([]byte(`{}`)) + ".", msg: "jose: not a JWT: the header is not a JSON object"},
		{name: "a payload that isn't an object", token: b64([]byte(`{"alg":"RS256"}`)) + "." + b64([]byte(`"text"`)) + ".", msg: "jose: not a JWT: the payload is not a JSON object"},
		{name: "a header without alg", token: b64([]byte(`{"typ":"JWT"}`)) + "." + b64([]byte(`{}`)) + ".", msg: "jose: not a JWT: the header has no alg"},
		{
			// RFC 7515 §4.1.11: a recipient that doesn't understand every critical extension must refuse the JWS.
			name: "a header with crit", token: b64([]byte(`{"alg":"RS256","crit":["b64"],"b64":false}`)) + "." + b64([]byte(`{}`)) + ".",
			msg: "jose: not a JWT: the header has crit extensions, which are not supported",
		},
		{name: "a kid that isn't a string", token: b64([]byte(`{"alg":"RS256","kid":1}`)) + "." + b64([]byte(`{}`)) + ".", msg: "jose: not a JWT: the kid of the header is not a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.token)
			require.ErrorIs(t, err, ErrMalformed)
			require.EqualError(t, err, tt.msg)
		})
	}
}

func TestCheckTime_RefusesATokenOutsideItsLifetime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)

	tests := []struct {
		name   string
		claims map[string]any
		is     error
		msg    string
	}{
		{name: "within its lifetime", claims: map[string]any{"nbf": 1_799_999_000.0, "exp": 1_800_000_001.0}},
		{name: "without exp or nbf", claims: make(map[string]any)},
		{
			// exp is the first instant the token is no longer valid (RFC 7519 §4.1.4).
			name: "at exp", claims: map[string]any{"exp": 1_800_000_000.0},
			is: ErrExpired, msg: "jose: token expired at 2027-01-15T08:00:00Z",
		},
		{
			name: "before nbf", claims: map[string]any{"nbf": 1_800_000_060.0},
			is: ErrNotYetValid, msg: "jose: token not yet valid until 2027-01-15T08:01:00Z",
		},
		{
			name: "an exp past the year 9999", claims: map[string]any{"exp": 1e300},
			is: ErrMalformed, msg: "jose: not a JWT: the exp claim is out of range",
		},
		{
			name: "an exp that isn't a number", claims: map[string]any{"exp": "tomorrow"},
			is: ErrMalformed, msg: "jose: not a JWT: the exp claim is not a number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&Token{Claims: tt.claims}).CheckTime(now)
			if tt.is == nil {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, tt.is)
			require.EqualError(t, err, tt.msg)
		})
	}
}
