// Package oidcapp is a small web app that signs users in with an OpenID Connect provider, through the authorization
// code flow with PKCE, and reads their profile from the provider's userinfo endpoint. It is the app under test of the
// OpenID Connect end-to-end tests: it knows nothing about vlatte, only the issuer it is given, and finds everything
// else through the provider's discovery document. It uses the standard library alone.
package oidcapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

var _ http.Handler = (*App)(nil)

// Cookies the app sets in the browser.
const (
	// LoginCookie holds the state, the nonce and the PKCE code verifier of a sign-in, from /login to /callback.
	LoginCookie   = "oidc_login"
	SessionCookie = "session"
)

var (
	// errSessionEnded is returned when the provider refuses the access token of a session: it was revoked or expired.
	errSessionEnded = errors.New("oidcapp: the provider refused the token, sign in again")
	// errOtherUser is returned when userinfo answers for another user than the ID token's.
	errOtherUser = errors.New("oidcapp: userinfo is for another user")
)

// Config is where the provider is and who the app is to it.
type Config struct {
	// IssuerURL is the issuer of the provider: its discovery document is at IssuerURL/.well-known/openid-configuration.
	IssuerURL string
	// ClientID and ClientSecret are the credentials of the app, which it sends with client_secret_post.
	ClientID     string
	ClientSecret string
	// RedirectURL is where the provider sends the browser back to: the /callback route of this app.
	RedirectURL string
	// Scope is what the app asks the user to grant, such as "openid email profile".
	Scope string
}

// App is the web app. Its cookies are Secure: serve it over HTTPS. Its routes:
//
//   - GET /login sends the browser to the provider to sign in, with a state, a nonce and a PKCE code challenge (S256).
//   - GET /callback is where the provider sends it back. It checks the state, exchanges the code and the code verifier
//     for tokens, validates the ID token, starts a session and sends the browser to /me.
//   - GET /me answers, as JSON, the sub, email and name of the ID token.
//   - GET /profile answers, as JSON, the sub, name and picture the userinfo endpoint answers for the access token.
//
// The ID token must be signed with RS256 by a key of the provider's JWKS, which the app fetches when it first needs
// it and again when a token names a key it doesn't know. Its issuer, audience, expiry and nonce must be the expected
// ones, and its email verified. Without a session, /me and /profile answer 401. When the provider refuses the access
// token, the app ends the session and answers 401.
type App struct {
	cfg    Config
	client *http.Client
	mux    *http.ServeMux

	mu       sync.Mutex
	provider *provider
	keys     map[string]*rsa.PublicKey // by kid
	sessions map[string]session        // by session ID
}

// provider is the part of the discovery document the app uses.
type provider struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

type session struct {
	Sub         string `json:"sub"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	accessToken string
}

// claims are the claims of an ID token the app reads.
type claims struct {
	Iss           string `json:"iss"`
	Aud           any    `json:"aud"`
	Azp           string `json:"azp"`
	Sub           string `json:"sub"`
	Exp           int64  `json:"exp"`
	Nonce         string `json:"nonce"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// New returns the app.
func New(cfg Config) *App {
	app := &App{
		cfg:      cfg,
		client:   &http.Client{Timeout: 10 * time.Second},
		mux:      http.NewServeMux(),
		keys:     make(map[string]*rsa.PublicKey),
		sessions: make(map[string]session),
	}

	app.mux.HandleFunc("GET /login", app.login)
	app.mux.HandleFunc("GET /callback", app.callback)
	app.mux.HandleFunc("GET /me", app.me)
	app.mux.HandleFunc("GET /profile", app.profile)

	return app
}

// ServeHTTP implements http.Handler.
func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	app.mux.ServeHTTP(w, r)
}

func (app *App) login(w http.ResponseWriter, r *http.Request) {
	p, err := app.discover(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	state, nonce := rand.Text(), rand.Text()
	verifier := rand.Text() + rand.Text() // 52 characters: RFC 7636 asks for 43 to 128
	http.SetCookie(w, &http.Cookie{
		Name: LoginCookie, Value: state + "." + nonce + "." + verifier, Path: "/", Secure: true, HttpOnly: true,
	})

	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {app.cfg.ClientID},
		"redirect_uri":          {app.cfg.RedirectURL},
		"scope":                 {app.cfg.Scope},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, p.AuthorizationEndpoint+"?"+query.Encode(), http.StatusFound)
}

func (app *App) callback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// The state must be the one this browser was given at /login, or the callback was forged: a CSRF attempt.
	var state, nonce, verifier string
	if cookie, err := r.Cookie(LoginCookie); err == nil {
		if parts := strings.Split(cookie.Value, "."); len(parts) == 3 {
			state, nonce, verifier = parts[0], parts[1], parts[2]
		}
	}
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(query.Get("state"))) != 1 {
		http.Error(w, "oidcapp: invalid state", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: LoginCookie, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true})

	if e := query.Get("error"); e != "" {
		http.Error(w, "oidcapp: sign in failed: "+e, http.StatusUnauthorized)
		return
	}

	accessToken, idToken, err := app.exchange(r.Context(), query.Get("code"), verifier)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	c, err := app.validate(r.Context(), idToken, nonce)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	id := rand.Text()
	app.mu.Lock()
	app.sessions[id] = session{Sub: c.Sub, Email: c.Email, Name: c.Name, accessToken: accessToken}
	app.mu.Unlock()

	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: id, Path: "/", Secure: true, HttpOnly: true})
	http.Redirect(w, r, "/me", http.StatusFound)
}

// exchange trades the code the provider gave the browser, and the verifier of the code challenge sent with the
// browser, for an access token and an ID token.
func (app *App) exchange(ctx context.Context, code, verifier string) (string, string, error) {
	p, err := app.discover(ctx)
	if err != nil {
		return "", "", err
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {app.cfg.RedirectURL},
		"client_id":     {app.cfg.ClientID},
		"client_secret": {app.cfg.ClientSecret},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("oidcapp: token exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := app.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("oidcapp: token exchange: %w", err)
	}
	defer res.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
	}
	if err := json.UnmarshalRead(res.Body, &body); err != nil {
		return "", "", fmt.Errorf("oidcapp: token exchange: status %d: %w", res.StatusCode, err)
	}

	if body.Error != "" {
		return "", "", fmt.Errorf("oidcapp: token exchange: %s", body.Error)
	}

	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("oidcapp: token exchange: status %d", res.StatusCode)
	}

	return body.AccessToken, body.IDToken, nil
}

// validate checks the ID token as OpenID Connect Core 3.1.3.7 asks, and returns its claims.
func (app *App) validate(ctx context.Context, token, nonce string) (*claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidcapp: id token: not a JWS")
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decode(parts[0], &header); err != nil {
		return nil, fmt.Errorf("oidcapp: id token: header: %w", err)
	}

	if header.Alg != "RS256" {
		return nil, fmt.Errorf("oidcapp: id token: alg %q, want RS256", header.Alg)
	}

	key, err := app.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("oidcapp: id token: signature: %w", err)
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return nil, errors.New("oidcapp: id token: bad signature")
	}

	var c claims
	if err := decode(parts[1], &c); err != nil {
		return nil, fmt.Errorf("oidcapp: id token: claims: %w", err)
	}

	p, err := app.discover(ctx)
	if err != nil {
		return nil, err
	}

	switch {
	case c.Iss != p.Issuer:
		return nil, errors.New("oidcapp: id token: wrong issuer")
	case !app.forMe(c):
		return nil, errors.New("oidcapp: id token: wrong audience")
	case !time.Now().Before(time.Unix(c.Exp, 0)):
		return nil, errors.New("oidcapp: id token: expired")
	case subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1:
		return nil, errors.New("oidcapp: id token: wrong nonce")
	case !c.EmailVerified:
		return nil, errors.New("oidcapp: id token: email not verified")
	}

	return &c, nil
}

// forMe tells whether the audience of c is this app: aud holds its client ID, and so does azp when aud holds more.
func (app *App) forMe(c claims) bool {
	var aud []string
	switch v := c.Aud.(type) {
	case string:
		aud = []string{v}
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok {
				aud = append(aud, s)
			}
		}
	}

	if !slices.Contains(aud, app.cfg.ClientID) {
		return false
	}

	if len(aud) > 1 || c.Azp != "" {
		return c.Azp == app.cfg.ClientID
	}

	return true
}

// key returns the provider's key kid, fetching the JWKS when it doesn't know it, as when the provider rotates keys.
func (app *App) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	app.mu.Lock()
	key, ok := app.keys[kid]
	app.mu.Unlock()

	if ok {
		return key, nil
	}

	keys, err := app.fetchKeys(ctx)
	if err != nil {
		return nil, err
	}

	app.mu.Lock()
	app.keys = keys
	app.mu.Unlock()

	if key, ok = keys[kid]; !ok {
		return nil, fmt.Errorf("oidcapp: id token: unknown kid %q", kid)
	}

	return key, nil
}

func (app *App) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	p, err := app.discover(ctx)
	if err != nil {
		return nil, err
	}

	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := app.get(ctx, p.JWKSURI, "", &set); err != nil {
		return nil, fmt.Errorf("oidcapp: jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}

		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if err := errors.Join(errN, errE); err != nil {
			return nil, fmt.Errorf("oidcapp: jwks: key %q: %w", k.Kid, err)
		}

		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}

	return keys, nil
}

// discover returns the provider's discovery document, fetched once.
func (app *App) discover(ctx context.Context) (*provider, error) {
	app.mu.Lock()
	p := app.provider
	app.mu.Unlock()

	if p != nil {
		return p, nil
	}

	p = new(provider)
	if err := app.get(ctx, app.cfg.IssuerURL+"/.well-known/openid-configuration", "", p); err != nil {
		return nil, fmt.Errorf("oidcapp: discovery: %w", err)
	}

	// The issuer the document names must be the one it was fetched from (OpenID Connect Discovery 4.3).
	if p.Issuer != app.cfg.IssuerURL {
		return nil, fmt.Errorf("oidcapp: discovery: issuer %q, want %q", p.Issuer, app.cfg.IssuerURL)
	}

	app.mu.Lock()
	app.provider = p
	app.mu.Unlock()

	return p, nil
}

func (app *App) me(w http.ResponseWriter, r *http.Request) {
	_, s, ok := app.session(r)
	if !ok {
		http.Error(w, "oidcapp: sign in first", http.StatusUnauthorized)
		return
	}

	write(w, s)
}

func (app *App) profile(w http.ResponseWriter, r *http.Request) {
	id, s, ok := app.session(r)
	if !ok {
		http.Error(w, "oidcapp: sign in first", http.StatusUnauthorized)
		return
	}

	p, err := app.discover(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	var info struct {
		Sub     string `json:"sub"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	err = app.get(r.Context(), p.UserInfoEndpoint, s.accessToken, &info)
	if err == nil && info.Sub != s.Sub {
		// It must not be used (OpenID Connect Core 5.3.2): it may be a substituted answer.
		err = errOtherUser
	}

	switch {
	case errors.Is(err, errSessionEnded):
		app.mu.Lock()
		delete(app.sessions, id)
		app.mu.Unlock()

		http.Error(w, err.Error(), http.StatusUnauthorized)
	case errors.Is(err, errOtherUser):
		http.Error(w, err.Error(), http.StatusBadGateway)
	case err != nil:
		http.Error(w, "oidcapp: userinfo: "+err.Error(), http.StatusBadGateway)
	default:
		write(w, info)
	}
}

func (app *App) session(r *http.Request) (string, session, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return "", session{}, false
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	s, ok := app.sessions[cookie.Value]

	return cookie.Value, s, ok
}

// get GETs u, with token as a bearer token when it isn't empty, and decodes the answer into out. A 401 is
// errSessionEnded.
func (app *App) get(ctx context.Context, u, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := app.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return errSessionEnded
	default:
		return fmt.Errorf("status %d", res.StatusCode)
	}

	return json.UnmarshalRead(res.Body, out)
}

// decode decodes s, base64url without padding, as JSON into out.
func decode(s string, out any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, out)
}

func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, v)
}
