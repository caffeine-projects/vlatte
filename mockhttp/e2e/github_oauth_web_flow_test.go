package e2e

import (
	"net/http"
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
	_ghToken        = "gho_e2e"
	// _ghScenario is the scenario of the codes GitHub issues: each code is an instance of its own, issued, then used.
	_ghScenario = "github-code"
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
		status, body := browse(t, browser, app.URL+"/callback?code=any&state="+url.QueryEscape(state))

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
	// history, must not sign anyone in, while a new sign-in, with a new code, still does.
	gh, app, browser := startGitHubOAuthWebFlow(t)

	callback := consent(t, browser, app)
	appURL, err := url.Parse(app.URL)
	require.NoError(t, err)
	cookies := browser.Jar.Cookies(appURL)

	status, _ := browse(t, browser, callback.String())
	require.Equal(t, http.StatusOK, status)

	// The replay brings back the state and the code verifier of the sign-in, so only the code being used tells it
	// apart.
	browser.Jar.SetCookies(appURL, cookies)

	status, body := browse(t, browser, callback.String())

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: token exchange: bad_verification_code\n", body)

	status, _ = browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	gh.exchange.AssertNumberOfCalls(t, 2)
	gh.badCode.AssertNumberOfCalls(t, 1)
}

func TestGitHubOAuthWebFlow_RejectsAnInjectedCode(t *testing.T) {
	// An attacker who gets hold of a user's code, such as from a log, and sends it to the app's callback in their own
	// browser, with their own state, must not be signed in as the user. The state can't tell: it is the attacker's.
	// PKCE does: GitHub issued the code for the user's code challenge, and the attacker's browser holds another
	// verifier.
	gh, app, user := startGitHubOAuthWebFlow(t)
	callback := consent(t, user, app)

	attacker := newBrowser(t, app)
	state := startSignIn(t, attacker, app)

	status, body := browse(t, attacker,
		app.URL+"/callback?code="+url.QueryEscape(callback.Query().Get("code"))+"&state="+url.QueryEscape(state))

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: token exchange: bad_verification_code\n", body)

	// The attempt didn't spend the code: the user still signs in with it.
	status, body = browse(t, user, callback.String())

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"login": "octocat", "name": "The Octocat"}`, body)

	gh.exchange.AssertNumberOfCalls(t, 1)
	gh.badCode.AssertNumberOfCalls(t, 1)
}

func TestGitHubOAuthWebFlow_EndsTheSessionWhenGitHubRevokesTheToken(t *testing.T) {
	// The user revokes the app on GitHub: the token stops working, and the app must sign the user out rather than
	// keep a session it can't serve.
	gh, app, browser := startGitHubOAuthWebFlow(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	revoked := gh.server.MustMock(mockhttp.Request().
		Method(http.MethodGet).
		Header("Authorization", match.StrictEqual("Bearer "+_ghToken)).
		Priority(-1).
		Reply(mockhttp.Unauthorized().JSON(map[string]string{
			"message":           "Bad credentials",
			"documentation_url": "https://docs.github.com/rest",
			"status":            "401",
		})))

	status, body := browse(t, browser, app.URL+"/repos")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: github refused the token, sign in again\n", body)

	status, body = browse(t, browser, app.URL+"/me")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "githubapp: sign in first\n", body)

	revoked.AssertNumberOfCalls(t, 1)
	gh.api.AssertNumberOfCalls(t, 1) // the sign-in's /me
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

// githubMocks are the mocks of GitHub, by what they answer.
type githubMocks struct {
	server *mockhttp.Server
	// authorize sends the browser back to the app with the code, as GitHub does once the user consents.
	authorize *mockhttp.Scope
	// exchange trades a code and the verifier of its code challenge for the token, once per code.
	exchange *mockhttp.Scope
	// badCode answers any other exchange, as GitHub does a code that is wrong, already used or not issued for the
	// code verifier.
	badCode *mockhttp.Scope
	// api answers the REST API calls made with the token.
	api *mockhttp.Scope
	// badCredentials answers the REST API calls made with another token or none. A call with the token the api mocks
	// don't match, such as one missing a header, matches no mock, so its test fails on what the call got wrong.
	badCredentials *mockhttp.Scope
}

// mockGitHub mocks the OAuth web flow of github.com and a few REST API calls of api.github.com. Each mock matches what
// GitHub requires of a request, so one the app gets wrong matches none and fails its test.
func mockGitHub(t *testing.T, server *mockhttp.Server, callback string) *githubMocks {
	t.Helper()

	gh := &githubMocks{server: server}

	// A code is made from the state, so each sign-in gets its own. The scenario instance of a code is keyed by the code
	// challenge it was issued for and the code. The exchange's key is the S256 of its code verifier and its code, the
	// same only when the verifier fits the challenge: the exchange then moves the instance from issued to used, so
	// PKCE and single use are one check.
	gh.authorize = server.MustMock(mockhttp.Getf("/login/oauth/authorize").
		Query("client_id", match.StrictEqual(_ghClientID)).
		Query("redirect_uri", match.StrictEqual(callback)).
		Query("scope", match.StrictEqual(_ghScope)).
		Query("state", match.Present()).
		Query("code_challenge", match.Present()).
		Query("code_challenge_method", match.StrictEqual("S256")).
		StartScenario(_ghScenario).
		ScenarioKey(`{{ .Request.URL.Query.Get "code_challenge" }}:gh-{{ .Request.URL.Query.Get "state" }}`).
		ScenarioStateWillBe("issued").
		Reply(mockhttp.Status(http.StatusFound).HeaderTemplate("Location",
			`{{ $state := .Request.URL.Query.Get "state" | urlquery }}`+
				`{{ .Request.URL.Query.Get "redirect_uri" }}?code=gh-{{ $state }}&state={{ $state }}`)))

	gh.exchange = server.MustMock(mockhttp.Postf("/login/oauth/access_token").
		Header("Accept", match.StrictEqual("application/json")).
		FormField("client_id", match.StrictEqual(_ghClientID)).
		FormField("client_secret", match.StrictEqual(_ghClientSecret)).
		FormField("redirect_uri", match.StrictEqual(callback)).
		ScenarioIs(_ghScenario).
		ScenarioKey(`{{ hash "sha256" "base64url" (.Request.PostForm.Get "code_verifier") }}:` +
			`{{ .Request.PostForm.Get "code" }}`).
		ScenarioStateIs("issued").
		ScenarioStateWillBe("used").
		Reply(mockhttp.OK().JSON(map[string]string{
			"access_token": _ghToken,
			"token_type":   "bearer",
			"scope":        _ghScope,
		})))

	// GitHub doesn't document its error for a code verifier that doesn't fit, so a wrong verifier gets the error of a
	// wrong code.
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
		Header("Authorization", match.Not(match.StrictEqual("Bearer "+_ghToken))).
		Priority(1).
		Reply(mockhttp.Unauthorized().JSON(map[string]string{
			"message":           "Bad credentials",
			"documentation_url": "https://docs.github.com/rest",
			"status":            "401",
		})))

	return gh
}
