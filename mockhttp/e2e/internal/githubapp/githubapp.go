// Package githubapp is a small web app that signs users in with GitHub, through the OAuth web flow of an OAuth app
// with PKCE, and reads their profile, repositories and issues with the GitHub REST API. It is the app under test of
// the GitHub end-to-end tests: it knows nothing about vlatte, only the base URLs of github.com and api.github.com it is
// given.
package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var _ http.Handler = (*App)(nil)

// Cookies the app sets in the browser.
const (
	StateCookie    = "oauth_state"
	VerifierCookie = "oauth_verifier"
	SessionCookie  = "session"
)

const _apiVersion = "2022-11-28"

// errSessionEnded is returned when GitHub refuses the token of a session: it was revoked or expired.
var errSessionEnded = errors.New("githubapp: github refused the token, sign in again")

// Config is where GitHub is and who the app is to it.
type Config struct {
	// AuthURL is the base URL of github.com, which serves the OAuth web flow.
	AuthURL string
	// APIURL is the base URL of api.github.com.
	APIURL string
	// ClientID and ClientSecret are the credentials of the OAuth app.
	ClientID     string
	ClientSecret string
	// RedirectURL is where GitHub sends the browser back to: the /callback route of this app.
	RedirectURL string
	// Scope is what the app asks the user to grant, such as "read:user repo".
	Scope string
}

// App is the web app. Its routes:
//
// Its cookies are Secure: serve it over HTTPS.
//
//   - GET /login sends the browser to GitHub to sign in, with a state and a PKCE code challenge (S256).
//   - GET /callback is where GitHub sends it back. It checks the state, exchanges the code and the code verifier for
//     a token, starts a session and sends the browser to /me.
//   - GET /me, GET /repos and GET /repos/{owner}/{repo}/issues answer, as JSON, the user's login and name, the full
//     names of their repositories and the titles of a repository's issues.
//
// Without a session, the API routes answer 401. When GitHub refuses the token, the app ends the session and
// answers 401.
type App struct {
	cfg    Config
	client *http.Client
	mux    *http.ServeMux

	mu       sync.Mutex
	sessions map[string]string // session ID to access token
}

// New returns the app.
func New(cfg Config) *App {
	app := &App{
		cfg:      cfg,
		client:   &http.Client{Timeout: 10 * time.Second},
		mux:      http.NewServeMux(),
		sessions: make(map[string]string),
	}

	app.mux.HandleFunc("GET /login", app.login)
	app.mux.HandleFunc("GET /callback", app.callback)
	app.mux.HandleFunc("GET /me", app.me)
	app.mux.HandleFunc("GET /repos", app.repos)
	app.mux.HandleFunc("GET /repos/{owner}/{repo}/issues", app.issues)

	return app
}

// ServeHTTP implements http.Handler.
func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	app.mux.ServeHTTP(w, r)
}

func (app *App) login(w http.ResponseWriter, r *http.Request) {
	state := rand.Text()
	verifier := rand.Text() + rand.Text() // 52 characters: RFC 7636 asks for 43 to 128
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: state, Path: "/", Secure: true, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: VerifierCookie, Value: verifier, Path: "/", Secure: true, HttpOnly: true})

	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"client_id":             {app.cfg.ClientID},
		"redirect_uri":          {app.cfg.RedirectURL},
		"scope":                 {app.cfg.Scope},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, app.cfg.AuthURL+"/login/oauth/authorize?"+query.Encode(), http.StatusFound)
}

func (app *App) callback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// The state must be the one this browser was given at /login, or the callback was forged: a CSRF attempt.
	cookie, err := r.Cookie(StateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(query.Get("state"))) != 1 {
		http.Error(w, "githubapp: invalid state", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: VerifierCookie, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true})

	if e := query.Get("error"); e != "" {
		http.Error(w, "githubapp: sign in failed: "+e, http.StatusUnauthorized)
		return
	}

	verifier, err := r.Cookie(VerifierCookie)
	if err != nil {
		http.Error(w, "githubapp: invalid state", http.StatusForbidden)
		return
	}

	token, err := app.exchange(r.Context(), query.Get("code"), verifier.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	id := rand.Text()
	app.mu.Lock()
	app.sessions[id] = token
	app.mu.Unlock()

	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: id, Path: "/", Secure: true, HttpOnly: true})
	http.Redirect(w, r, "/me", http.StatusFound)
}

// exchange trades the code GitHub gave the browser, and the verifier of the code challenge sent with the browser,
// for an access token.
func (app *App) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"client_id":     {app.cfg.ClientID},
		"client_secret": {app.cfg.ClientSecret},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {app.cfg.RedirectURL},
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, app.cfg.AuthURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("githubapp: token exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, err := app.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("githubapp: token exchange: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("githubapp: token exchange: status %d", res.StatusCode)
	}

	// GitHub answers a failed exchange with a 200 too, with error set.
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.UnmarshalRead(res.Body, &body); err != nil {
		return "", fmt.Errorf("githubapp: token exchange: %w", err)
	}

	if body.Error != "" {
		return "", fmt.Errorf("githubapp: token exchange: %s", body.Error)
	}

	return body.AccessToken, nil
}

func (app *App) me(w http.ResponseWriter, r *http.Request) {
	var user struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if !app.call(w, r, "/user", &user) {
		return
	}

	write(w, user)
}

func (app *App) repos(w http.ResponseWriter, r *http.Request) {
	var repos []struct {
		FullName string `json:"full_name"`
	}
	if !app.call(w, r, "/user/repos", &repos) {
		return
	}

	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, repo.FullName)
	}

	write(w, names)
}

func (app *App) issues(w http.ResponseWriter, r *http.Request) {
	var issues []struct {
		Title string `json:"title"`
	}
	path := "/repos/" + url.PathEscape(r.PathValue("owner")) + "/" + url.PathEscape(r.PathValue("repo")) + "/issues"
	if !app.call(w, r, path, &issues) {
		return
	}

	titles := make([]string, 0, len(issues))
	for _, issue := range issues {
		titles = append(titles, issue.Title)
	}

	write(w, titles)
}

// call GETs path of the API with the token of the request's session, and decodes the answer into out. It answers the
// request itself and returns false when there is no session or the call fails.
func (app *App) call(w http.ResponseWriter, r *http.Request, path string, out any) bool {
	id, token, ok := app.session(r)
	if !ok {
		http.Error(w, "githubapp: sign in first", http.StatusUnauthorized)
		return false
	}

	err := app.get(r.Context(), token, path, out)
	if errors.Is(err, errSessionEnded) {
		app.mu.Lock()
		delete(app.sessions, id)
		app.mu.Unlock()

		http.Error(w, err.Error(), http.StatusUnauthorized)

		return false
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return false
	}

	return true
}

func (app *App) session(r *http.Request) (string, string, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return "", "", false
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	token, ok := app.sessions[cookie.Value]

	return cookie.Value, token, ok
}

func (app *App) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, app.cfg.APIURL+path, nil)
	if err != nil {
		return fmt.Errorf("githubapp: GET %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", _apiVersion)

	res, err := app.client.Do(req)
	if err != nil {
		return fmt.Errorf("githubapp: GET %s: %w", path, err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return errSessionEnded
	default:
		return fmt.Errorf("githubapp: GET %s: status %d", path, res.StatusCode)
	}

	if err := json.UnmarshalRead(res.Body, out); err != nil {
		return fmt.Errorf("githubapp: GET %s: %w", path, err)
	}

	return nil
}

func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalWrite(w, v)
}
