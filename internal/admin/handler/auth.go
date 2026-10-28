package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
)

const (
	// _basicChallenge asks for Basic credentials: a browser shows its sign-in prompt for it.
	_basicChallenge = `Basic realm="vlatte admin", charset="UTF-8"`

	// _bearerChallenge asks for a bearer token.
	_bearerChallenge = `Bearer realm="vlatte admin"`
)

var _ http.Handler = (*authenticator)(nil)

// Credentials are what the admin API asks every request for, but those of its probes. The zero value asks for none.
type Credentials struct {
	// Username and Password are the credentials of Basic. Empty, Basic takes only the token.
	Username string
	Password string

	// Token is the bearer token: in an Authorization: Bearer header, or as the password of Basic, with any username,
	// as a browser sends it. Empty asks for none.
	Token string
}

// Required reports whether c asks for credentials.
func (c Credentials) Required() bool {
	return c.Username != "" || c.Token != ""
}

// authenticator answers 401 to the requests that lack the credentials it asks for.
type authenticator struct {
	next   http.Handler
	open   map[string]bool // the method and path of the requests that need no credentials, such as "GET /livez"
	basic  bool
	user   [sha256.Size]byte
	pass   [sha256.Size]byte
	bearer bool
	token  [sha256.Size]byte
}

// Authenticate returns a handler that passes to next the requests with the credentials c asks for, and those of open,
// patterns of a method and a path such as "GET /livez", which match a request of that method, or HEAD for GET, and
// that exact path. It answers every other request 401, with the challenges of Basic, which a browser shows its
// sign-in prompt for, and of Bearer when c has a token. Credentials are compared in constant time.
func Authenticate(c Credentials, open []string, next http.Handler) http.Handler {
	a := &authenticator{next: next, open: make(map[string]bool, len(open))}
	for _, pattern := range open {
		a.open[pattern] = true
	}

	if c.Username != "" {
		a.basic, a.user, a.pass = true, sha256.Sum256([]byte(c.Username)), sha256.Sum256([]byte(c.Password))
	}

	if c.Token != "" {
		a.bearer, a.token = true, sha256.Sum256([]byte(c.Token))
	}

	return a
}

func (a *authenticator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.isOpen(r) {
		a.next.ServeHTTP(w, r)
		return
	}

	sent, ok := a.check(r)
	if ok {
		a.next.ServeHTTP(w, r)
		return
	}

	log := zerolog.Ctx(r.Context()).Debug()
	if sent {
		log = zerolog.Ctx(r.Context()).Warn()
	}

	log.Str("remote", r.RemoteAddr).Str("method", r.Method).Str("path", r.URL.Path).
		Msg("admin: a request without valid credentials")

	h := w.Header()
	h.Add("WWW-Authenticate", _basicChallenge)

	if a.bearer {
		challenge := _bearerChallenge
		if _, bearer := bearerToken(r); bearer {
			challenge += `, error="invalid_token"`
		}

		h.Add("WWW-Authenticate", challenge)
	}

	h.Set("Cache-Control", "no-store")

	fail(w, r, &problemError{
		status: http.StatusUnauthorized, code: _codeUnauthorized, detail: "the admin API needs valid credentials",
	})
}

// isOpen reports whether r needs no credentials.
func (a *authenticator) isOpen(r *http.Request) bool {
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}

	return a.open[method+" "+r.URL.Path]
}

// check reports whether r sent credentials, and whether they are the ones a asks for.
func (a *authenticator) check(r *http.Request) (sent, ok bool) {
	if token, bearer := bearerToken(r); bearer {
		return true, a.bearer && equal(a.token, token) == 1
	}

	user, pass, basic := r.BasicAuth()
	if !basic {
		return false, false
	}

	// Each comparison runs whatever the others found, so the time taken tells nothing of which failed.
	match := 0
	if a.basic {
		match |= equal(a.user, user) & equal(a.pass, pass)
	}

	if a.bearer {
		match |= equal(a.token, pass)
	}

	return true, match == 1
}

// bearerToken returns the token of the Authorization: Bearer header of r, whose scheme is matched in any case, and
// whether it has one.
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	return strings.TrimLeft(token, " "), true
}

// equal returns 1 when the SHA-256 of given is want, and 0 otherwise, in time that depends on neither: hashing first
// hides the length of what is compared.
func equal(want [sha256.Size]byte, given string) int {
	got := sha256.Sum256([]byte(given))
	return subtle.ConstantTimeCompare(want[:], got[:])
}
