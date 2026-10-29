package e2e

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/mockhttp/e2e/internal/githubapp"
)

// These tests run a "Sign in with GitHub" app, githubapp, against GitHub mocked with vlatte, through the OAuth web
// flow of an OAuth app: the browser goes to github.com to consent, comes back to the app with a code, and the app
// exchanges the code for a token it then calls api.github.com with. One vlatte server stands in for both hosts.
//
// The mocks are declared here with mockhttp alone, at the bottom of the file, not with the githubauth recipe: these
// tests check that vlatte can stand in for an auth provider, whatever the recipe does. Other ways to authenticate,
// such as the device flow, get their own file.

const (
	_ghClientID     = "e2e-client-id"
	_ghClientSecret = "e2e-client-secret"
	_ghScope        = "read:user repo"
	_ghCode         = "e2e-code"
	_ghToken        = "gho_e2e"
)

func TestGitHubOAuthWebFlow_SignsInAndReadsTheUsersData(t *testing.T) {
	gh, app, browser := startGitHubOAuthWebFlow(t)

	// The browser follows the redirects: to GitHub, back to the app's callback, then to /me.
	status, body := browse(t, browser, app.URL+"/login")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"login": "octocat", "name": "The Octocat"}`, body)

	status, body = browse(t, browser, app.URL+"/repos")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `["octocat/hello-world", "octocat/spoon-knife"]`, body)

	status, body = browse(t, browser, app.URL+"/repos/octocat/hello-world/issues")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `["Found a bug", "Add a README"]`, body)

	gh.authorize.AssertNumberOfCalls(t, 1)
	gh.exchange.AssertNumberOfCalls(t, 1)
	gh.api.AssertNumberOfCalls(t, 3)
	gh.badCode.AssertNotCalled(t)
	gh.badCredentials.AssertNotCalled(t)
}

func TestGitHubOAuthWebFlow_RejectsAForgedState(t *testing.T) {
	// A callback with a state the browser wasn't given at /login is a CSRF attempt: an attacker's code would sign the
	// user in to the attacker's account. The app must refuse it before it spends the code.
	gh, app, browser := startGitHubOAuthWebFlow(t)
	startSignIn(t, browser, app)

	// A state the app gave another browser is as forged as a made-up one.
	for _, state := range []string{"forged", startSignIn(t, newBrowser(t, app), app)} {
		status, body := browse(t, browser, app.URL+"/callback?code="+_ghCode+"&state="+url.QueryEscape(state))

		require.Equal(t, http.StatusForbidden, status)
		require.Equal(t, "githubapp: invalid state\n", body)
	}

	status, body := browse(t, browser, app.URL+"/me")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: sign in first\n", body)

	gh.exchange.AssertNotCalled(t)
	gh.badCode.AssertNotCalled(t)
}

func TestGitHubOAuthWebFlow_RejectsADeniedConsent(t *testing.T) {
	// The user clicks Cancel on GitHub's consent page: GitHub sends the browser back with an error and no code.
	gh, app, browser := startGitHubOAuthWebFlow(t)
	denied := gh.server.MustMock(mockhttp.Getf("/login/oauth/authorize").
		Priority(-1).
		Reply(mockhttp.Status(http.StatusFound).HeaderTemplate("Location",
			`{{ .Request.URL.Query.Get "redirect_uri" }}?error=access_denied&state={{ .Request.URL.Query.Get "state" | urlquery }}`)))

	status, body := browse(t, browser, app.URL+"/login")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: sign in failed: access_denied\n", body)

	denied.AssertNumberOfCalls(t, 1)
	gh.exchange.AssertNotCalled(t)
	gh.badCode.AssertNotCalled(t)
	gh.api.AssertNotCalled(t)
}

func TestGitHubOAuthWebFlow_RejectsAReusedCode(t *testing.T) {
	// GitHub codes are single-use: a callback replayed with a code already exchanged, such as from the browser's
	// history, must not sign anyone in.
	gh, app, browser := startGitHubOAuthWebFlow(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	// The replay gets a fresh state, so only the code is stale.
	state := startSignIn(t, browser, app)

	status, body := browse(t, browser, app.URL+"/callback?code="+_ghCode+"&state="+url.QueryEscape(state))

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: token exchange: bad_verification_code\n", body)

	gh.exchange.AssertNumberOfCalls(t, 1)
	gh.badCode.AssertNumberOfCalls(t, 1)
}

func TestGitHubOAuthWebFlow_EndsTheSessionWhenGitHubRevokesTheToken(t *testing.T) {
	// The user revokes the app on GitHub: the token stops working, and the app must sign the user out rather than
	// keep a session it can't serve.
	gh, app, browser := startGitHubOAuthWebFlow(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	gh.api.Disable()

	status, body := browse(t, browser, app.URL+"/repos")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: github refused the token, sign in again\n", body)

	status, body = browse(t, browser, app.URL+"/me")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: sign in first\n", body)

	gh.badCredentials.AssertNumberOfCalls(t, 1)
}

// startGitHubOAuthWebFlow starts the GitHub mocks, the app pointed at them, and a browser.
func startGitHubOAuthWebFlow(t *testing.T) (*githubMocks, *httptest.Server, *http.Client) {
	t.Helper()

	server := mockhttp.MustNew(mockhttp.Setup().Silent())
	server.MustStart()
	t.Cleanup(server.Close)

	// The app must know its own URL, its redirect URL, before it serves. It serves HTTPS, as its cookies are Secure.
	app := httptest.NewUnstartedServer(nil)
	callback := "https://" + app.Listener.Addr().String() + "/callback"
	app.Config.Handler = githubapp.New(githubapp.Config{
		AuthURL:      server.URL(),
		APIURL:       server.URL(),
		ClientID:     _ghClientID,
		ClientSecret: _ghClientSecret,
		RedirectURL:  callback,
		Scope:        _ghScope,
	})
	app.StartTLS()
	t.Cleanup(app.Close)

	return mockGitHub(t, server, callback), app, newBrowser(t, app)
}

// newBrowser returns a client that keeps cookies and follows redirects, as a browser does, and trusts app.
func newBrowser(t *testing.T, app *httptest.Server) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	browser := &http.Client{Jar: jar, Transport: app.Client().Transport}
	t.Cleanup(browser.CloseIdleConnections)

	return browser
}

// noRedirects returns a client that shares the cookies of browser but stops at the first redirect.
func noRedirects(browser *http.Client) *http.Client {
	return &http.Client{
		Jar:           browser.Jar,
		Transport:     browser.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// startSignIn sends browser to /login, stops it at the redirect to GitHub, and returns the state the app gave it.
func startSignIn(t *testing.T, browser *http.Client, app *httptest.Server) string {
	t.Helper()

	res, err := noRedirects(browser).Get(app.URL + "/login")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusFound, res.StatusCode)

	location, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)

	return location.Query().Get("state")
}

func browse(t *testing.T, browser *http.Client, u string) (int, string) {
	t.Helper()

	res, err := browser.Get(u)
	require.NoError(t, err)
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(b)
}

// githubMocks are the mocks of GitHub, by what they answer.
type githubMocks struct {
	server *mockhttp.Server
	// authorize sends the browser back to the app with the code, as GitHub does once the user consents.
	authorize *mockhttp.Scope
	// exchange trades the code for the token, once.
	exchange *mockhttp.Scope
	// badCode answers any other exchange, as GitHub does a code that is wrong or already used.
	badCode *mockhttp.Scope
	// api answers the REST API calls made with the token.
	api *mockhttp.Scope
	// badCredentials answers the REST API calls made without the token.
	badCredentials *mockhttp.Scope
}

// mockGitHub mocks the OAuth web flow of github.com and a few REST API calls of api.github.com. Each mock matches what
// GitHub requires of a request, so one the app gets wrong matches none and fails its test.
func mockGitHub(t *testing.T, server *mockhttp.Server, callback string) *githubMocks {
	t.Helper()

	gh := &githubMocks{server: server}

	gh.authorize = server.MustMock(mockhttp.Getf("/login/oauth/authorize").
		Query("client_id", match.StrictEqual(_ghClientID)).
		Query("redirect_uri", match.StrictEqual(callback)).
		Query("scope", match.StrictEqual(_ghScope)).
		Query("state", match.Present()).
		Reply(mockhttp.Status(http.StatusFound).HeaderTemplate("Location",
			`{{ .Request.URL.Query.Get "redirect_uri" }}?code=`+_ghCode+`&state={{ .Request.URL.Query.Get "state" | urlquery }}`)))

	gh.exchange = server.MustMock(mockhttp.Postf("/login/oauth/access_token").
		Header("Accept", match.StrictEqual("application/json")).
		FormField("client_id", match.StrictEqual(_ghClientID)).
		FormField("client_secret", match.StrictEqual(_ghClientSecret)).
		FormField("code", match.StrictEqual(_ghCode)).
		FormField("redirect_uri", match.StrictEqual(callback)).
		Once().
		Reply(mockhttp.OK().JSON(map[string]string{
			"access_token": _ghToken,
			"token_type":   "bearer",
			"scope":        _ghScope,
		})))

	gh.badCode = server.MustMock(mockhttp.Postf("/login/oauth/access_token").
		Priority(1).
		Reply(mockhttp.OK().JSON(map[string]string{
			"error":             "bad_verification_code",
			"error_description": "The code passed is incorrect or expired.",
		})))

	api := func(path string) *mockhttp.Builder {
		return mockhttp.Request().
			Method(http.MethodGet).
			URLPath(match.StrictEqual(path)).
			Header("Authorization", match.StrictEqual("Bearer "+_ghToken)).
			Header("Accept", match.StrictEqual("application/vnd.github+json")).
			Header("X-GitHub-Api-Version", match.StrictEqual("2022-11-28"))
	}
	gh.api = server.MustMock(
		api("/user").Reply(mockhttp.OK().JSON(map[string]any{
			"id":    1,
			"login": "octocat",
			"name":  "The Octocat",
		})),
		api("/user/repos").Reply(mockhttp.OK().JSON([]map[string]any{
			{"id": 1296269, "full_name": "octocat/hello-world", "private": false},
			{"id": 1300192, "full_name": "octocat/spoon-knife", "private": false},
		})),
		api("/repos/octocat/hello-world/issues").Reply(mockhttp.OK().JSON([]map[string]any{
			{"number": 1347, "title": "Found a bug", "state": "open"},
			{"number": 1348, "title": "Add a README", "state": "open"},
		})),
	)

	gh.badCredentials = server.MustMock(mockhttp.Request().
		Method(http.MethodGet).
		URLPath(match.Either(match.HasPrefix("/user"), match.HasPrefix("/repos/"))).
		Priority(1).
		Reply(mockhttp.Unauthorized().JSON(map[string]string{
			"message":           "Bad credentials",
			"documentation_url": "https://docs.github.com/rest",
			"status":            "401",
		})))

	return gh
}
