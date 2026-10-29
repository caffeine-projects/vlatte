package oidc_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/jose"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/recipe/http/oidc"
)

func TestOIDC_DiscoveryAnnouncesTheEndpointsOnTheIssuer(t *testing.T) {
	m := newServer(t)

	status, body := get(t, m.URL("/.well-known/openid-configuration"), "")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{
		"issuer": "`+m.URL()+`",
		"authorization_endpoint": "`+m.URL("/authorize")+`",
		"token_endpoint": "`+m.URL("/token")+`",
		"jwks_uri": "`+m.URL("/jwks")+`",
		"userinfo_endpoint": "`+m.URL("/userinfo")+`",
		"response_types_supported": ["code"],
		"grant_types_supported": ["authorization_code"],
		"subject_types_supported": ["public"],
		"id_token_signing_alg_values_supported": ["ES256"],
		"scopes_supported": ["openid", "email", "profile"],
		"token_endpoint_auth_methods_supported": ["client_secret_post"],
		"code_challenge_methods_supported": ["S256"]
	}`, body)
}

func TestOIDC_SignsInWithPKCEAndANonce(t *testing.T) {
	m := newServer(t)

	code, state := authorize(t, m, url.Values{"state": {"st-1"}, "nonce": {"n-1"}, "code_challenge": {challenge("v-1")}, "code_challenge_method": {"S256"}})
	require.Equal(t, "st-1", state)

	status, tokens := exchange(t, m, code, "v-1")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, oidc.DefaultAccessToken, tokens["access_token"])
	require.Equal(t, "Bearer", tokens["token_type"])
	require.InDelta(t, 3600, tokens["expires_in"], 0)

	// A client verifies the ID token with the provider's JWKS, as it fetches it.
	claims := verify(t, m, tokens["id_token"].(string))

	exp, iat := claims["exp"].(float64), claims["iat"].(float64)
	delete(claims, "exp")
	delete(claims, "iat")
	require.Equal(t, map[string]any{
		"iss":            m.URL(),
		"aud":            oidc.DefaultClientID,
		"sub":            "vlatte-user",
		"email":          "user@vlatte.example",
		"email_verified": true,
		"name":           "VLatte User",
		"nonce":          "n-1",
	}, claims)
	require.InDelta(t, time.Now().Unix(), iat, 5)
	require.InDelta(t, time.Now().Add(time.Hour).Unix(), exp, 5)
}

func TestOIDC_ExchangesACodeOnce(t *testing.T) {
	// A code replayed, from the browser's history or by an attacker who saw it, must not get a token.
	m := newServer(t)
	code, _ := authorize(t, m, url.Values{"code_challenge": {challenge("v-1")}})

	status, _ := exchange(t, m, code, "v-1")
	require.Equal(t, http.StatusOK, status)

	status, body := exchange(t, m, code, "v-1")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_grant", body["error"])
}

func TestOIDC_RefusesACodeVerifierThatDoesntFitTheChallenge(t *testing.T) {
	// PKCE: a code stolen on its way to the app is useless without the verifier only the app has.
	m := newServer(t)
	code, _ := authorize(t, m, url.Values{"code_challenge": {challenge("v-1")}})

	for _, verifier := range []string{"stolen", ""} {
		status, body := exchange(t, m, code, verifier)
		require.Equal(t, http.StatusBadRequest, status, "verifier %q", verifier)
		require.Equal(t, "invalid_grant", body["error"])
	}

	// The failures used nothing: the app's own exchange still works.
	status, _ := exchange(t, m, code, "v-1")
	require.Equal(t, http.StatusOK, status)
}

func TestOIDC_RefusesAnotherSignInsCode(t *testing.T) {
	// Two sign-ins, each with its own challenge: one's code with the other's verifier is a code injection.
	m := newServer(t)
	codeA, _ := authorize(t, m, url.Values{"code_challenge": {challenge("v-a")}})
	codeB, _ := authorize(t, m, url.Values{"code_challenge": {challenge("v-b")}})
	require.NotEqual(t, codeA, codeB)

	status, _ := exchange(t, m, codeA, "v-b")
	require.Equal(t, http.StatusBadRequest, status)

	status, _ = exchange(t, m, codeB, "v-b")
	require.Equal(t, http.StatusOK, status)
}

func TestOIDC_SignsInWithoutPKCEOrANonce(t *testing.T) {
	m := newServer(t)
	code, state := authorize(t, m, nil)
	require.Empty(t, state)

	status, tokens := exchange(t, m, code, "")
	require.Equal(t, http.StatusOK, status)

	claims := verify(t, m, tokens["id_token"].(string))
	require.NotContains(t, claims, "nonce")
}

func TestOIDC_UserInfoAnswersTheClaimsForTheAccessToken(t *testing.T) {
	m := newServer(t, oidc.AccessToken("at-1"), oidc.Claims(map[string]any{"sub": "ana", "picture": "https://p.example/ana.png"}))

	status, body := get(t, m.URL("/userinfo"), "Bearer at-1")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"sub": "ana", "picture": "https://p.example/ana.png"}`, body)

	res := do(t, http.MethodGet, m.URL("/userinfo"), "Bearer other", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, `Bearer error="invalid_token"`, res.Header.Get(mockhttp.HeaderWWWAuthenticate))
}

func TestOIDC_Options(t *testing.T) {
	m := newServer(t,
		oidc.ClientID("app"), oidc.ClientSecret("s3"), oidc.Issuer("https://id.example"), oidc.TokenTTL(5*time.Minute),
		oidc.WithPaths(oidc.Paths{Authorize: "/o/oauth2/v2/auth", Token: "/oauth2/token"}))

	_, body := get(t, m.URL("/.well-known/openid-configuration"), "")

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	require.Equal(t, "https://id.example", doc["issuer"])
	require.Equal(t, "https://id.example/o/oauth2/v2/auth", doc["authorization_endpoint"])
	require.Equal(t, "https://id.example/oauth2/token", doc["token_endpoint"])
	require.Equal(t, "https://id.example/jwks", doc["jwks_uri"])

	res := do(t, http.MethodGet, m.URL("/o/oauth2/v2/auth")+"?"+url.Values{
		"response_type": {"code"}, "client_id": {"app"}, "redirect_uri": {"https://app.example/cb"}, "scope": {"openid"},
	}.Encode(), "", nil)
	require.Equal(t, http.StatusFound, res.StatusCode)

	location, err := url.Parse(res.Header.Get(mockhttp.HeaderLocation))
	require.NoError(t, err)

	res = do(t, http.MethodPost, m.URL("/oauth2/token"), "", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"app"}, "client_secret": {"s3"},
		"redirect_uri": {"https://app.example/cb"}, "code": {location.Query().Get("code")},
	})
	require.Equal(t, http.StatusOK, res.StatusCode)

	var tokens map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &tokens))
	require.InDelta(t, 300, tokens["expires_in"], 0)

	claims := verify(t, m, tokens["id_token"].(string))
	require.Equal(t, "https://id.example", claims["iss"])
	require.Equal(t, "app", claims["aud"])
}

func TestOIDC_SignsWithTheKeyItIsGiven(t *testing.T) {
	// A server can hold several keys: the recipe signs with the one KeyID names, and discovery announces that key's
	// algorithm, so a client that checks it accepts the tokens.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateKeys(oidc.DefaultKeyID+"=RS256", "ec=ES256"))
	require.NoError(t, m.Prep(t.Context(), oidc.New(oidc.KeyID("ec"))))
	m.MustStart()
	t.Cleanup(m.Close)

	_, body := get(t, m.URL("/.well-known/openid-configuration"), "")

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &doc))
	require.Equal(t, []any{"ES256"}, doc["id_token_signing_alg_values_supported"])

	code, _ := authorize(t, m, nil)
	status, tokens := exchange(t, m, code, "")
	require.Equal(t, http.StatusOK, status)

	tok, err := jose.Parse(tokens["id_token"].(string))
	require.NoError(t, err)
	require.Equal(t, "ES256", tok.Alg)
	require.Equal(t, "ec", tok.Kid)
}

func TestOIDC_KeepsTheClaimsItWasGiven(t *testing.T) {
	// A test that reuses its claims map for another server must not change what this one answers.
	claims := map[string]any{"sub": "ana"}
	m := newServer(t, oidc.Claims(claims))
	claims["sub"] = "bob"

	code, _ := authorize(t, m, nil)
	_, tokens := exchange(t, m, code, "")
	status, body := get(t, m.URL("/userinfo"), "Bearer "+oidc.DefaultAccessToken)

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"sub": "ana"}`, body)
	require.Equal(t, "ana", verify(t, m, tokens["id_token"].(string))["sub"])
}

func TestOIDC_AuthorizeNeedsAnOpenIDScopeAndTheClient(t *testing.T) {
	m := newServer(t)

	for name, query := range map[string]url.Values{
		"no openid scope":  {"response_type": {"code"}, "client_id": {oidc.DefaultClientID}, "redirect_uri": {"https://a/cb"}, "scope": {"email"}},
		"another client":   {"response_type": {"code"}, "client_id": {"other"}, "redirect_uri": {"https://a/cb"}, "scope": {"openid"}},
		"implicit flow":    {"response_type": {"id_token"}, "client_id": {oidc.DefaultClientID}, "redirect_uri": {"https://a/cb"}, "scope": {"openid"}},
		"no redirect_uri":  {"response_type": {"code"}, "client_id": {oidc.DefaultClientID}, "scope": {"openid"}},
		"openid in a word": {"response_type": {"code"}, "client_id": {oidc.DefaultClientID}, "redirect_uri": {"https://a/cb"}, "scope": {"openidx"}},
	} {
		res := do(t, http.MethodGet, m.URL("/authorize")+"?"+query.Encode(), "", nil)
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, name)
	}
}

func TestOIDC_PrepFailsOnAServerWithoutTheKey(t *testing.T) {
	// The recipe can't add a key to the server's engine: it must say which one is missing, not sign nothing.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())

	err := m.Prep(t.Context(), oidc.New())
	require.ErrorContains(t, err, `"vlatte" is not a signing key of the server`)
}

func TestOIDC_WorksWithAKeyFromTheSettings(t *testing.T) {
	// The setup the package doc shows: a key generated from template.keys.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateKeys(oidc.DefaultKeyID + "=RS256"))
	require.NoError(t, m.Prep(t.Context(), oidc.New()))
	m.MustStart()
	t.Cleanup(m.Close)

	code, _ := authorize(t, m, nil)
	status, tokens := exchange(t, m, code, "")
	require.Equal(t, http.StatusOK, status)

	tok, err := jose.Parse(tokens["id_token"].(string))
	require.NoError(t, err)
	require.Equal(t, "RS256", tok.Alg)
}

// Generators

// newServer starts a server with an ES256 key as oidc.DefaultKeyID, prepared with the recipe.
func newServer(t *testing.T, opts ...oidc.Option) *mockhttp.Server {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	engine := vlatte.MustNewGoTemplateEngine(vlatte.WithSigningKey(oidc.DefaultKeyID, "ES256", key))
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(engine))
	require.NoError(t, m.Prep(t.Context(), oidc.New(opts...)))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// authorize sends the authorize request of a sign-in, with extra on the required parameters, and returns the code and
// state the redirect carries.
func authorize(t *testing.T, m *mockhttp.Server, extra url.Values) (string, string) {
	t.Helper()

	query := url.Values{
		"response_type": {"code"},
		"client_id":     {oidc.DefaultClientID},
		"redirect_uri":  {"https://app.example/callback"},
		"scope":         {"openid email profile"},
	}
	maps.Copy(query, extra)

	res := do(t, http.MethodGet, m.URL("/authorize")+"?"+query.Encode(), "", nil)
	require.Equal(t, http.StatusFound, res.StatusCode)

	location, err := url.Parse(res.Header.Get(mockhttp.HeaderLocation))
	require.NoError(t, err)
	require.Equal(t, "https://app.example/callback", location.Scheme+"://"+location.Host+location.Path)

	return location.Query().Get("code"), location.Query().Get("state")
}

// exchange sends code to the token endpoint, with verifier as code_verifier unless it is empty.
func exchange(t *testing.T, m *mockhttp.Server, code, verifier string) (int, map[string]any) {
	t.Helper()

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {oidc.DefaultClientID},
		"client_secret": {oidc.DefaultClientSecret},
		"redirect_uri":  {"https://app.example/callback"},
		"code":          {code},
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}

	res := do(t, http.MethodPost, m.URL("/token"), "", form)

	var body map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &body))

	return res.StatusCode, body
}

// verify returns the claims of token once it verifies with the server's JWKS.
func verify(t *testing.T, m *mockhttp.Server, token string) map[string]any {
	t.Helper()

	_, jwks := get(t, m.URL("/jwks"), "")

	ks, err := jose.ParseKeySet([]byte(jwks))
	require.NoError(t, err)

	tok, err := jose.Parse(token)
	require.NoError(t, err)
	require.NoError(t, ks.Verify(tok))
	require.Equal(t, oidc.DefaultKeyID, tok.Kid)

	return tok.Claims
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func get(t *testing.T, u, authorization string) (int, string) {
	t.Helper()

	res := do(t, http.MethodGet, u, authorization, nil)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(b)
}

// _noRedirects is a client that stops at a redirect, so the test reads where the authorize endpoint sends the browser.
var _noRedirects = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func do(t *testing.T, method, u, authorization string, form url.Values) *http.Response {
	t.Helper()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(t.Context(), method, u, body)
	require.NoError(t, err)

	if form != nil {
		req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEFormURLEncoded)
	}

	if authorization != "" {
		req.Header.Set(mockhttp.HeaderAuthorization, authorization)
	}

	res, err := _noRedirects.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}
