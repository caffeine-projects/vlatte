package oidcapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The end-to-end tests drive the app through a mocked Google and cover what it checks of an ID token. These cover
// what it does when the provider fails or answers what it can't read.

// _unreachable is a URL no connection reaches, on any system: 65535 is the last port.
const _unreachable = "http://127.0.0.1:65536"

func TestApp_CallbackRefusesWhatItCannotUse(t *testing.T) {
	tests := []struct {
		name string
		// setup changes the provider and returns the ID token its token endpoint answers.
		setup func(p *fakeProvider) string
		body  string
	}{
		{
			name: "a token endpoint no connection reaches",
			setup: func(p *fakeProvider) string {
				p.doc["token_endpoint"] = _unreachable + "/token"
				return p.idToken(nil)
			},
			body: `oidcapp: token exchange: Post "` + _unreachable + `/token": dial tcp: address 65536: invalid port`,
		},
		{
			name: "a token endpoint that isn't a URL",
			setup: func(p *fakeProvider) string {
				p.doc["token_endpoint"] = "::"
				return p.idToken(nil)
			},
			body: `oidcapp: token exchange: parse "::": missing protocol scheme`,
		},
		{
			name: "a token answer that isn't JSON",
			setup: func(p *fakeProvider) string {
				p.token = func(w http.ResponseWriter) { _, _ = w.Write([]byte("oops")) }
				return ""
			},
			body: "oidcapp: token exchange: status 200: jsontext: invalid character 'o' at start of value",
		},
		{
			name: "a failed token answer without an error code",
			setup: func(p *fakeProvider) string {
				p.token = func(w http.ResponseWriter) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte("{}"))
				}
				return ""
			},
			body: "oidcapp: token exchange: status 500",
		},
		{
			name:  "an ID token that isn't a JWS",
			setup: func(*fakeProvider) string { return "a.b" },
			body:  "oidcapp: id token: not a JWS",
		},
		{
			name:  "a header that isn't base64url",
			setup: func(*fakeProvider) string { return "%%%.e30.c2ln" },
			body:  "oidcapp: id token: header: illegal base64 data at input byte 0",
		},
		{
			name: "a signature that isn't base64url",
			setup: func(p *fakeProvider) string {
				token := p.idToken(nil)
				return token[:strings.LastIndex(token, ".")] + ".%%%"
			},
			body: "oidcapp: id token: signature: illegal base64 data at input byte 0",
		},
		{
			name:  "claims that aren't JSON",
			setup: func(p *fakeProvider) string { return p.sign([]byte("not json")) },
			body:  "oidcapp: id token: claims: jsontext: invalid character 'o' in literal null (expecting 'u') after offset 1",
		},
		{
			name: "a JWKS the fakeProvider fails to serve",
			setup: func(p *fakeProvider) string {
				p.jwks = func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }
				return p.idToken(nil)
			},
			body: "oidcapp: jwks: status 500",
		},
		{
			name: "a JWKS key that isn't base64url",
			setup: func(p *fakeProvider) string {
				p.jwks = func(w http.ResponseWriter) {
					_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"key-1","n":"%%%","e":"AQAB"}]}`))
				}
				return p.idToken(nil)
			},
			body: `oidcapp: jwks: key "key-1": illegal base64 data at input byte 0`,
		},
		{
			// With more than one audience, azp says which party the token was issued to (OpenID Connect Core 2).
			name:  "audiences that name the app, without azp",
			setup: func(p *fakeProvider) string { return p.idToken(map[string]any{"aud": []any{_clientID, "other-app"}}) },
			body:  "oidcapp: id token: wrong audience",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newFakeProvider(t)
			p.idTokenOf = tt.setup(p)

			rec := p.callback(t, New(p.config()))
			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Equal(t, tt.body+"\n", rec.Body.String())
		})
	}
}

func TestApp_CallbackSignsIn(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		jwks   func(p *fakeProvider) []any
	}{
		{name: "the app as the only audience, without azp", claims: map[string]any{"aud": _clientID}},
		{name: "audiences with azp naming the app", claims: map[string]any{"aud": []any{_clientID, "other-app"}, "azp": _clientID}},
		{
			// A provider may publish keys of other types too: the app reads its RSA ones.
			name: "a JWKS with a key of another type",
			jwks: func(p *fakeProvider) []any {
				return []any{map[string]any{"kty": "EC", "kid": "ec-1", "crv": "P-256", "x": "AA", "y": "AA"}, p.jwk()}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newFakeProvider(t)
			p.idTokenOf = p.idToken(tt.claims)

			if tt.jwks != nil {
				keys := tt.jwks(p)
				p.jwks = func(w http.ResponseWriter) { _ = json.MarshalWrite(w, map[string]any{"keys": keys}) }
			}

			rec := p.callback(t, New(p.config()))
			require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
			require.Equal(t, "/me", rec.Header().Get("Location"))
		})
	}
}

func TestApp_LoginNeedsTheDiscoveryDocument(t *testing.T) {
	t.Run("of an issuer no connection reaches", func(t *testing.T) {
		rec := serve(t, New(Config{IssuerURL: _unreachable}), "/login", nil)
		require.Equal(t, http.StatusBadGateway, rec.Code)
		require.Equal(t, `oidcapp: discovery: Get "`+_unreachable+`/.well-known/openid-configuration": dial tcp: address 65536: invalid port`+"\n",
			rec.Body.String())
	})

	t.Run("that names another issuer", func(t *testing.T) {
		// A document that names another issuer may come from an impostor (OpenID Connect Discovery 4.3).
		p := newFakeProvider(t)
		p.doc["issuer"] = "https://accounts.example.com"

		rec := serve(t, New(p.config()), "/login", nil)
		require.Equal(t, http.StatusBadGateway, rec.Code)
		require.Equal(t, `oidcapp: discovery: issuer "https://accounts.example.com", want "`+p.srv.URL+`"`+"\n", rec.Body.String())
	})
}

func TestApp_AnswersOnlyASignedInBrowser(t *testing.T) {
	app := New(newFakeProvider(t).config())

	for _, path := range []string{"/me", "/profile"} {
		rec := serve(t, app, path, nil)
		require.Equal(t, http.StatusUnauthorized, rec.Code, path)
		require.Equal(t, "oidcapp: sign in first\n", rec.Body.String(), path)
	}
}

func TestApp_ProfileFailsWhenUserinfoDoes(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		userinfo func(w http.ResponseWriter)
		body     string
	}{
		{
			name:     "an endpoint that fails",
			userinfo: func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) },
			body:     "oidcapp: userinfo: status 500",
		},
		{
			name:     "an endpoint no connection reaches",
			endpoint: _unreachable + "/userinfo",
			body:     `oidcapp: userinfo: Get "` + _unreachable + `/userinfo": dial tcp: address 65536: invalid port`,
		},
		{
			name:     "an endpoint that isn't a URL",
			endpoint: "::",
			body:     `oidcapp: userinfo: parse "::": missing protocol scheme`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newFakeProvider(t)
			p.idTokenOf = p.idToken(nil)

			if tt.endpoint != "" {
				p.doc["userinfo_endpoint"] = tt.endpoint
			}

			if tt.userinfo != nil {
				p.userinfo = tt.userinfo
			}

			app := New(p.config())
			signedIn := p.callback(t, app)
			require.Equal(t, http.StatusFound, signedIn.Code, signedIn.Body.String())

			rec := serve(t, app, "/profile", signedIn.Result().Cookies())
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.Equal(t, tt.body+"\n", rec.Body.String())
		})
	}
}

// Generators

const (
	_clientID = "app"
	_kid      = "key-1"
	_nonce    = "nonce-1"
	_state    = "state-1"
)

// _providerKey is the provider's signing key, made once: RSA keys are slow to make, more so with the race detector.
var _providerKey = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 2048) })

// fakeProvider is an OpenID Connect provider an httptest server serves. A test changes its discovery document, or what
// its endpoints answer, before the app reads them.
type fakeProvider struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	doc map[string]any
	// idTokenOf is the ID token the token endpoint answers.
	idTokenOf string
	token     func(w http.ResponseWriter)
	jwks      func(w http.ResponseWriter)
	userinfo  func(w http.ResponseWriter)
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()

	key, err := _providerKey()
	require.NoError(t, err)

	p := &fakeProvider{key: key}
	p.token = func(w http.ResponseWriter) {
		_ = json.MarshalWrite(w, map[string]any{"access_token": "access-1", "id_token": p.idTokenOf})
	}
	p.jwks = func(w http.ResponseWriter) { _ = json.MarshalWrite(w, map[string]any{"keys": []any{p.jwk()}}) }
	p.userinfo = func(w http.ResponseWriter) { _ = json.MarshalWrite(w, map[string]any{"sub": "user-1"}) }

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.MarshalWrite(w, p.doc)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, _ *http.Request) { p.token(w) })
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) { p.jwks(w) })
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, _ *http.Request) { p.userinfo(w) })

	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)

	p.doc = map[string]any{
		"issuer":                 p.srv.URL,
		"authorization_endpoint": p.srv.URL + "/authorize",
		"token_endpoint":         p.srv.URL + "/token",
		"jwks_uri":               p.srv.URL + "/jwks",
		"userinfo_endpoint":      p.srv.URL + "/userinfo",
	}

	return p
}

func (p *fakeProvider) config() Config {
	return Config{IssuerURL: p.srv.URL, ClientID: _clientID, RedirectURL: "https://app.example.com/callback"}
}

// jwk is the public half of the provider's key, as its JWKS publishes it.
func (p *fakeProvider) jwk() map[string]any {
	b64 := base64.RawURLEncoding.EncodeToString

	return map[string]any{"kty": "RSA", "kid": _kid, "alg": "RS256", "n": b64(p.key.N.Bytes()), "e": "AQAB"}
}

// idToken returns an ID token the app accepts, but for the claims given, which replace its own.
func (p *fakeProvider) idToken(claims map[string]any) string {
	c := map[string]any{
		"iss":            p.srv.URL,
		"aud":            _clientID,
		"sub":            "user-1",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"nonce":          _nonce,
		"email_verified": true,
	}
	maps.Copy(c, claims)

	payload, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}

	return p.sign(payload)
}

// sign returns payload signed with the provider's key, as an RS256 compact JWS.
func (p *fakeProvider) sign(payload []byte) string {
	b64 := base64.RawURLEncoding.EncodeToString
	input := b64([]byte(`{"alg":"RS256","kid":"`+_kid+`"}`)) + "." + b64(payload)

	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(nil, p.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}

	return input + "." + b64(sig)
}

// callback sends the browser back to app from the provider, as the login of _state and _nonce would.
func (p *fakeProvider) callback(t *testing.T, app *App) *httptest.ResponseRecorder {
	t.Helper()

	login := &http.Cookie{Name: LoginCookie, Value: _state + "." + _nonce + ".verifier"}

	return serve(t, app, "/callback?state="+_state+"&code=code-1", []*http.Cookie{login})
}

// serve GETs path from app with cookies.
func serve(t *testing.T, app *App, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	return rec
}
