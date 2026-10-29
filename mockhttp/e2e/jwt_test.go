package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

// TestJWTFromMockFiles_IssuesAndChecksTokens runs a server declared in files alone, testdata/jwt: its config gives it
// signing keys, and its mocks issue tokens with them and serve an API only to a token the issuer signed. No Go code
// signs or checks a token. The issuer's key is made by the test, not kept in the repository, so the test writes it,
// and its public half where the API mock names ISSUER_PUBLIC_KEY.
func TestJWTFromMockFiles_IssuesAndChecksTokens(t *testing.T) {
	server, _ := startJWTServer(t)

	tests := []struct {
		name string
		// path and form are the token request; an empty path sends no token to the API.
		path   string
		form   url.Values
		status int
		body   string
	}{
		{
			name:   "a token the issuer signed for the API",
			path:   "/oauth/token",
			form:   url.Values{"grant_type": {"client_credentials"}, "audience": {"orders"}, "expires_in": {"1h"}},
			status: http.StatusOK,
			body:   `[{"id": 1, "total": 42}]`,
		},
		{
			name:   "a token signed by a key the API doesn't trust",
			path:   "/rogue/token",
			status: http.StatusUnauthorized,
		},
		{
			name:   "an expired token",
			path:   "/oauth/token",
			form:   url.Values{"grant_type": {"client_credentials"}, "audience": {"orders"}, "expires_in": {"-1h"}},
			status: http.StatusUnauthorized,
		},
		{
			name:   "a token for another API",
			path:   "/oauth/token",
			form:   url.Values{"grant_type": {"client_credentials"}, "audience": {"payments"}, "expires_in": {"1h"}},
			status: http.StatusUnauthorized,
		},
		{
			name:   "no token",
			status: http.StatusUnauthorized,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL()+"/orders", nil)
			require.NoError(t, err)

			if tt.path != "" {
				req.Header.Set(mockhttp.HeaderAuthorization, "Bearer "+issueToken(t, server.URL()+tt.path, tt.form))
			}

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)

			require.Equal(t, tt.status, res.StatusCode)

			if tt.body == "" {
				require.Empty(t, b)
				return
			}

			require.JSONEq(t, tt.body, string(b))
		})
	}
}

func TestJWTFromMockFiles_PublishesTheIssuersPublicKey(t *testing.T) {
	// A client checks the tokens with the keys the issuer publishes: the JWKS must hold the public half of the key the
	// tokens are signed with, under the kid their header names.
	server, key := startJWTServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL()+"/.well-known/jwks.json", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, http.StatusOK, res.StatusCode)

	var jwks map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &jwks))

	// The uncompressed point of a P-256 key is 0x04, then x and y, 32 bytes each.
	point, err := key.PublicKey.Bytes()
	require.NoError(t, err)

	b64 := base64.RawURLEncoding.EncodeToString
	require.Equal(t, map[string]any{"keys": []any{map[string]any{
		"kty": "EC",
		"use": "sig",
		"alg": "ES256",
		"kid": "issuer",
		"crv": "P-256",
		"x":   b64(point[1:33]),
		"y":   b64(point[33:]),
	}}}, jwks)
}

// startJWTServer writes testdata/jwt and a new issuer key to a directory, starts a server from its config, and
// returns it with the key.
func startJWTServer(t *testing.T) (*mockhttp.Server, *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	private, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	// The API mock names the issuer's public key ISSUER_PUBLIC_KEY.
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata/jwt")))

	orders := filepath.Join(dir, "mocks", "orders.yaml")
	mock, err := os.ReadFile(orders)
	require.NoError(t, err)
	mock = bytes.ReplaceAll(mock, []byte("ISSUER_PUBLIC_KEY"), []byte(strconv.Quote(string(publicPEM))))
	require.NoError(t, os.WriteFile(orders, mock, 0o600))

	require.NoError(t, os.Mkdir(filepath.Join(dir, "keys"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keys", "issuer.pem"),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0o600))

	server := mockhttp.MustNew(mockhttp.Setup().
		Silent().
		RootDir(dir).
		ConfigSources(mockhttp.ConfigFile(filepath.Join(dir, ".vlatterc.yaml"))))
	server.MustStart()
	t.Cleanup(server.Close)

	return server, key
}

// issueToken POSTs form to the token endpoint u and returns the access token it answers.
func issueToken(t *testing.T, u string, form url.Values) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, u, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEFormURLEncoded)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, http.StatusOK, res.StatusCode)

	var body struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.UnmarshalRead(res.Body, &body))

	return body.AccessToken
}
