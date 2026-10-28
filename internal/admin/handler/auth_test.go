package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

const (
	_basicChallenge  = `Basic realm="vlatte admin", charset="UTF-8"`
	_bearerChallenge = `Bearer realm="vlatte admin"`
)

func TestAuthenticate_RefusesARequestWithoutCredentials(t *testing.T) {
	// Anyone who reaches the admin API could change the mocks: without credentials, nothing runs, and the challenges
	// tell a browser to show its sign-in prompt, and an API client which schemes it takes.
	url := serveAuth(t, handler.Credentials{Username: "ops", Password: "pw", Token: "tok"})

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks"})

	problem(t, res, body, http.StatusUnauthorized, "unauthorized")
	require.Equal(t, []string{_basicChallenge, _bearerChallenge}, res.Header.Values("WWW-Authenticate"))
	require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
}

func TestAuthenticate_RefusesAnUnknownPathBeforeRouting(t *testing.T) {
	// A 404 would tell a stranger which routes exist: every path needs credentials.
	url := serveAuth(t, handler.Credentials{Token: "tok"})

	res, body := send(t, request{method: http.MethodGet, url: url + "/nothing-here"})

	problem(t, res, body, http.StatusUnauthorized, "unauthorized")
}

func TestAuthenticate_BasicNeedsTheUsernameAndThePassword(t *testing.T) {
	url := serveAuth(t, handler.Credentials{Username: "ops", Password: "pw"})

	tests := []struct {
		name     string
		username string
		password string
		status   int
	}{
		{name: "both right", username: "ops", password: "pw", status: http.StatusOK},
		{name: "wrong username", username: "root", password: "pw", status: http.StatusUnauthorized},
		{name: "wrong password", username: "ops", password: "pW", status: http.StatusUnauthorized},
		{name: "a longer password", username: "ops", password: "pw ", status: http.StatusUnauthorized},
		{name: "empty", username: "", password: "", status: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.SetBasicAuth(tc.username, tc.password) }})
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}

func TestAuthenticate_BasicOnlyTakesNoBearerToken(t *testing.T) {
	// The password of Basic is no token: an admin API that asks for Basic alone doesn't take it as one.
	url := serveAuth(t, handler.Credentials{Username: "ops", Password: "pw"})

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.Header.Set("Authorization", "Bearer pw") }})

	problem(t, res, body, http.StatusUnauthorized, "unauthorized")
	require.Equal(t, []string{_basicChallenge}, res.Header.Values("WWW-Authenticate"))
}

func TestAuthenticate_BearerTakesTheToken(t *testing.T) {
	url := serveAuth(t, handler.Credentials{Token: "tok"})

	tests := []struct {
		name   string
		header string
		status int
	}{
		{name: "the token", header: "Bearer tok", status: http.StatusOK},
		{name: "any case of the scheme", header: "bearer tok", status: http.StatusOK},
		{name: "a wrong token", header: "Bearer tak", status: http.StatusUnauthorized},
		{name: "a prefix of the token", header: "Bearer to", status: http.StatusUnauthorized},
		{name: "no token", header: "Bearer ", status: http.StatusUnauthorized},
		{name: "another scheme", header: "Token tok", status: http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.Header.Set("Authorization", tc.header) }})
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}

func TestAuthenticate_AWrongTokenIsInvalid(t *testing.T) {
	// RFC 6750 tells a client whose token was refused apart from one that sent none.
	url := serveAuth(t, handler.Credentials{Token: "tok"})

	res, _ := send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.Header.Set("Authorization", "Bearer tak") }})

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, []string{_basicChallenge, _bearerChallenge + `, error="invalid_token"`}, res.Header.Values("WWW-Authenticate"))
}

func TestAuthenticate_BrowsersSendTheTokenAsThePassword(t *testing.T) {
	// A browser sends no bearer token to a page, nor from EventSource: its sign-in prompt takes the token as the
	// password, with any username, and sends it on with every request of the pages.
	url := serveAuth(t, handler.Credentials{Token: "tok"})

	res, _ := send(t, request{method: http.MethodGet, url: url + "/events", set: func(r *http.Request) { r.SetBasicAuth("anyone", "tok") }})
	require.Equal(t, http.StatusNotImplemented, res.StatusCode, "past the check: this server streams no events")

	res, _ = send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.SetBasicAuth("anyone", "tak") }})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestAuthenticate_BasicAndBearerEachPass(t *testing.T) {
	url := serveAuth(t, handler.Credentials{Username: "ops", Password: "pw", Token: "tok"})

	res, _ := send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.SetBasicAuth("ops", "pw") }})
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = send(t, request{method: http.MethodGet, url: url + "/mocks", set: func(r *http.Request) { r.Header.Set("Authorization", "Bearer tok") }})
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestAuthenticate_TakesNoTokenInTheQuery(t *testing.T) {
	// A URL ends up in logs, histories and Referer headers: a token in it would leak.
	url := serveAuth(t, handler.Credentials{Token: "tok"})

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks?access_token=tok"})

	problem(t, res, body, http.StatusUnauthorized, "unauthorized")
}

func TestAuthenticate_OnlyTheProbePathsAreOpen(t *testing.T) {
	// A path that passes through a probe's is another path: it needs credentials.
	url := serveAuth(t, handler.Credentials{Token: "tok"})

	for _, path := range []string{"/livez/../mocks", "/livez/", "/LIVEZ"} {
		res, body := send(t, request{method: http.MethodGet, url: url + path})
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, path, body)
	}

	res, _ := send(t, request{method: http.MethodPost, url: url + "/livez"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode, "only GET is open")
}

func TestAuthenticate_CrossOriginRequestsStayRefused(t *testing.T) {
	// A browser sends the credentials of its sign-in prompt on its own, even to a hostile site's form: the cross-origin
	// check still refuses what would change the server.
	url := serveAuth(t, handler.Credentials{Username: "ops", Password: "pw"})

	res, body := send(t, request{method: http.MethodDelete, url: url + "/mocks", set: func(r *http.Request) {
		r.SetBasicAuth("ops", "pw")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}})

	problem(t, res, body, http.StatusForbidden, "cross_origin")
}

// serveAuth starts an admin API that asks for c, of an empty store, with the capabilities of caps, if any, and returns
// its URL.
func serveAuth(t *testing.T, c handler.Credentials, caps ...adminapi.Capabilities) string {
	t.Helper()

	var capabilities adminapi.Capabilities
	if len(caps) > 0 {
		capabilities = caps[0]
	}

	return start(t, admin.Options{Auth: c}, newStore(t), &fakeBuilder{}, capabilities).URL()
}
