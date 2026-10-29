package githubauth_test

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/recipe/http/githubauth"
)

func newServer(t *testing.T, opts ...githubauth.Option) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	require.NoError(t, m.Prep(t.Context(), githubauth.New(opts...)))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// _noRedirects is a client that stops at a redirect, so the test reads where the authorize endpoint sends the browser.
var _noRedirects = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func authorize(t *testing.T, m *mockhttp.Server, query url.Values) *http.Response {
	t.Helper()

	res, err := _noRedirects.Get(m.URL(githubauth.AuthorizePath) + "?" + query.Encode())
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

func exchange(t *testing.T, m *mockhttp.Server, form url.Values) map[string]string {
	t.Helper()

	res := post(t, m, form, "application/json")
	defer res.Body.Close()

	// GitHub answers errors of the token exchange with a 200 too: an app must read the body to tell them apart.
	require.Equal(t, http.StatusOK, res.StatusCode)

	var body map[string]string
	require.NoError(t, json.UnmarshalRead(res.Body, &body))

	return body
}

func post(t *testing.T, m *mockhttp.Server, form url.Values, accept string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, m.URL(githubauth.AccessTokenPath), strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	return res
}

func get(t *testing.T, m *mockhttp.Server, path, authorization string) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, m.URL(path), nil)
	require.NoError(t, err)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(b)
}

func TestAuthorize_RedirectsBackWithTheCodeAndTheState(t *testing.T) {
	// The app checks the state it gets back against the one it sent, to refuse forged callbacks: the recipe must hand
	// it back untouched, escaped or not.
	m := newServer(t)

	testCases := []struct {
		name        string
		redirectURI string
		state       string
		location    string
	}{
		{
			name:        "state",
			redirectURI: "http://app.test/callback",
			state:       "xyz",
			location:    "http://app.test/callback?code=vlatte-code&state=xyz",
		},
		{
			name:        "state to escape",
			redirectURI: "http://app.test/callback",
			state:       "a b&c",
			location:    "http://app.test/callback?code=vlatte-code&state=a+b%26c",
		},
		{
			name:        "redirect_uri with a query",
			redirectURI: "http://app.test/callback?next=home",
			state:       "xyz",
			location:    "http://app.test/callback?next=home&code=vlatte-code&state=xyz",
		},
		{
			name:        "no state",
			redirectURI: "http://app.test/callback",
			location:    "http://app.test/callback?code=vlatte-code",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{
				"client_id":    {githubauth.DefaultClientID},
				"redirect_uri": {tc.redirectURI},
				"scope":        {"read:user"},
			}
			if tc.state != "" {
				query.Set("state", tc.state)
			}

			res := authorize(t, m, query)

			require.Equal(t, http.StatusFound, res.StatusCode)
			require.Equal(t, tc.location, res.Header.Get("Location"))
		})
	}
}

func TestAuthorize_RefusesAnotherClient(t *testing.T) {
	// No mock matches another client ID, so an app configured with the wrong one fails its test.
	m := newServer(t)

	res := authorize(t, m, url.Values{"client_id": {"other"}, "redirect_uri": {"http://app.test/callback"}})

	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestAccessToken_ExchangesTheCodeForTheToken(t *testing.T) {
	m := newServer(t)

	body := exchange(t, m, url.Values{
		"client_id":     {githubauth.DefaultClientID},
		"client_secret": {githubauth.DefaultClientSecret},
		"code":          {githubauth.DefaultCode},
	})

	require.Equal(t, map[string]string{
		"access_token": githubauth.DefaultToken,
		"token_type":   "bearer",
		"scope":        githubauth.DefaultScope,
	}, body)
}

func TestAccessToken_AnswersJSONOnlyToAppsThatAskForIt(t *testing.T) {
	// GitHub answers a form-encoded body unless the app sends Accept: application/json, so an app that reads JSON
	// without asking for it would pass against a mock that always answers JSON and fail against GitHub.
	m := newServer(t)
	form := url.Values{
		"client_id":     {githubauth.DefaultClientID},
		"client_secret": {githubauth.DefaultClientSecret},
		"code":          {githubauth.DefaultCode},
	}

	res := post(t, m, form, "")
	require.NoError(t, res.Body.Close())

	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestAccessToken_AnswersGitHubsErrors(t *testing.T) {
	// An app must handle the errors GitHub answers, in GitHub's shape, to be tested against the recipe.
	m := newServer(t)

	testCases := []struct {
		name     string
		clientID string
		secret   string
		code     string
		error    string
	}{
		{
			name:     "wrong code",
			clientID: githubauth.DefaultClientID,
			secret:   githubauth.DefaultClientSecret,
			code:     "expired",
			error:    "bad_verification_code",
		},
		{
			name:     "wrong secret",
			clientID: githubauth.DefaultClientID,
			secret:   "wrong",
			code:     githubauth.DefaultCode,
			error:    "incorrect_client_credentials",
		},
		{
			name:     "wrong client ID",
			clientID: "wrong",
			secret:   githubauth.DefaultClientSecret,
			code:     githubauth.DefaultCode,
			error:    "incorrect_client_credentials",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			body := exchange(t, m, url.Values{
				"client_id":     {tc.clientID},
				"client_secret": {tc.secret},
				"code":          {tc.code},
			})

			require.Equal(t, tc.error, body["error"])
			require.NotContains(t, body, "access_token")
		})
	}
}

func TestUser_AnswersTheProfileToTheToken(t *testing.T) {
	m := newServer(t)

	for _, scheme := range []string{"Bearer", "token"} {
		status, body := get(t, m, githubauth.UserPath, scheme+" "+githubauth.DefaultToken)

		require.Equal(t, http.StatusOK, status)
		require.JSONEq(t, `{
			"id": 1,
			"login": "octocat",
			"name": "The Octocat",
			"email": "octocat@github.com",
			"avatar_url": "https://github.com/images/error/octocat_happy.gif",
			"html_url": "https://github.com/octocat",
			"type": "User"
		}`, body)
	}

	status, body := get(t, m, githubauth.UserEmailsPath, "Bearer "+githubauth.DefaultToken)

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `[{"email": "octocat@github.com", "primary": true, "verified": true, "visibility": "public"}]`, body)
}

func TestUser_RefusesAMissingOrWrongToken(t *testing.T) {
	// GitHub answers 401, Bad credentials, to a revoked or wrong token: an app must sign the user out on it.
	m := newServer(t)

	for _, path := range []string{githubauth.UserPath, githubauth.UserEmailsPath} {
		for _, authorization := range []string{"", "Bearer wrong", githubauth.DefaultToken} {
			status, body := get(t, m, path, authorization)

			require.Equal(t, http.StatusUnauthorized, status, "%s %q", path, authorization)
			require.JSONEq(t, `{
				"message": "Bad credentials",
				"documentation_url": "https://docs.github.com/rest",
				"status": "401"
			}`, body)
		}
	}
}

func TestOptions_ReplaceTheDefaults(t *testing.T) {
	user := githubauth.User{ID: 7, Login: "hubot", Name: "Hubot", Type: "Bot"}
	emails := []githubauth.Email{{Email: "hubot@example.com", Verified: true, Visibility: "private"}}
	m := newServer(t,
		githubauth.ClientID("id"),
		githubauth.ClientSecret("secret"),
		githubauth.Code("code"),
		githubauth.Token("token"),
		githubauth.Scope("repo"),
		githubauth.WithUser(user),
		githubauth.Emails(emails...),
	)

	res := authorize(t, m, url.Values{"client_id": {"id"}, "redirect_uri": {"http://app.test/callback"}})
	require.Equal(t, "http://app.test/callback?code=code", res.Header.Get("Location"))

	body := exchange(t, m, url.Values{"client_id": {"id"}, "client_secret": {"secret"}, "code": {"code"}})
	require.Equal(t, map[string]string{"access_token": "token", "token_type": "bearer", "scope": "repo"}, body)

	status, profile := get(t, m, githubauth.UserPath, "Bearer token")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"id": 7, "login": "hubot", "name": "Hubot", "email": "", "avatar_url": "", "html_url": "", "type": "Bot"}`, profile)

	status, addresses := get(t, m, githubauth.UserEmailsPath, "Bearer token")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `[{"email": "hubot@example.com", "primary": false, "verified": true, "visibility": "private"}]`, addresses)
}

func TestEmails_AreNoneForAUserWithoutAnEmail(t *testing.T) {
	// A profile without an email has no address to list: an app reading the primary address must find none, not "".
	m := newServer(t, githubauth.WithUser(githubauth.User{ID: 7, Login: "hubot"}))

	status, body := get(t, m, githubauth.UserEmailsPath, "Bearer "+githubauth.DefaultToken)

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `[]`, body)
}
