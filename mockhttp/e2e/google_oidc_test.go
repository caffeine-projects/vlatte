package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/mockhttp/e2e/internal/oidcapp"
)

// These tests run a "Sign in with Google" app, oidcapp, against Google mocked with vlatte, through the OpenID Connect
// authorization code flow with PKCE: the app finds Google's endpoints through discovery, sends the browser to Google
// to consent, exchanges the code it comes back with for an ID token, and checks the token's signature with Google's
// published keys. One vlatte server stands in for Google's hosts and is the issuer.
//
// The mocks are declared here with mockhttp alone, at the bottom of the file, not with the oidc recipe: these tests
// check that vlatte can stand in for an OpenID Connect provider, whatever the recipe does. Every mock is declared,
// none is Go code: the ID tokens are signed by templates, with keys the server holds.

const (
	_googleClientID     = "e2e-client.apps.googleusercontent.com"
	_googleClientSecret = "e2e-client-secret"
	_googleScope        = "openid email profile"
	_googleAccessToken  = "ya29.e2e"
	_googleSub          = "110169484474386276334"
	// _googleScenario is the scenario of the codes Google issues: each code is an instance of its own, issued, then
	// used.
	_googleScenario = "google-code"
)

// _googleKeys are the server's signing keys, by kid: key-1, which Google publishes, key-2, which it publishes once it
// rotates keys, and rogue, which it never publishes. They are made once per run, as RSA keys are slow to make.
var _googleKeys = sync.OnceValues(func() (map[string]*rsa.PrivateKey, error) {
	keys := make(map[string]*rsa.PrivateKey)
	for _, kid := range []string{"key-1", "key-2", "rogue"} {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}

		keys[kid] = key
	}

	return keys, nil
})

// _googleUser is who signs in: the claims of the ID token about the user, which userinfo answers too.
var _googleUser = map[string]any{
	"sub":            _googleSub,
	"email":          "octocat@gmail.com",
	"email_verified": true,
	"name":           "The Octocat",
	"given_name":     "The",
	"family_name":    "Octocat",
	"picture":        "https://lh3.googleusercontent.com/a/octocat",
}

func TestGoogleOIDC_SignsInWithAVerifiedIDToken(t *testing.T) {
	g, app, browser := startGoogleOIDC(t)

	// The browser follows the redirects: to Google, back to the app's callback, then to /me.
	status, body := browse(t, browser, app.URL+"/login")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"sub": "110169484474386276334", "email": "octocat@gmail.com", "name": "The Octocat"}`, body)

	// Another sign-in gets a code of its own, and the app keeps what it learned of Google.
	status, _ = browse(t, newBrowser(t, app), app.URL+"/login")

	require.Equal(t, http.StatusOK, status)

	g.discovery.AssertNumberOfCalls(t, 1)
	g.jwks.AssertNumberOfCalls(t, 1)
	g.authorize.AssertNumberOfCalls(t, 2)
	g.token.AssertNumberOfCalls(t, 2)
	g.badGrant.AssertNotCalled(t)
}

func TestGoogleOIDC_RejectsAForgedState(t *testing.T) {
	// A callback with a state the browser wasn't given at /login is a CSRF attempt: an attacker's code would sign the
	// user in to the attacker's account. The app must refuse it before it spends the code.
	g, app, browser := startGoogleOIDC(t)
	startSignIn(t, browser, app)

	// A state the app gave another browser is as forged as a made-up one.
	for _, state := range []string{"forged", startSignIn(t, newBrowser(t, app), app)} {
		status, body := browse(t, browser, app.URL+"/callback?code=any&state="+url.QueryEscape(state))

		require.Equal(t, http.StatusForbidden, status)
		require.Equal(t, "oidcapp: invalid state\n", body)
	}

	status, body := browse(t, browser, app.URL+"/me")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "oidcapp: sign in first\n", body)

	g.token.AssertNotCalled(t)
	g.badGrant.AssertNotCalled(t)
}

func TestGoogleOIDC_RejectsADeniedConsent(t *testing.T) {
	// The user clicks Cancel on Google's consent page: Google sends the browser back with an error and no code.
	g, app, browser := startGoogleOIDC(t)
	denied := g.server.MustMock(mockhttp.Getf("/o/oauth2/v2/auth").
		Priority(-1).
		Reply(mockhttp.Status(http.StatusFound).HeaderTemplate("Location",
			`{{ .Request.URL.Query.Get "redirect_uri" }}?error=access_denied&state={{ .Request.URL.Query.Get "state" | urlquery }}`)))

	status, body := browse(t, browser, app.URL+"/login")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "oidcapp: sign in failed: access_denied\n", body)

	denied.AssertNumberOfCalls(t, 1)
	g.token.AssertNotCalled(t)
	g.badGrant.AssertNotCalled(t)
	g.jwks.AssertNotCalled(t)
}

func TestGoogleOIDC_RejectsAnInjectedCode(t *testing.T) {
	// An attacker who gets hold of a user's code, such as from a log, and sends it to the app's callback in their own
	// browser, with their own state, must not be signed in as the user. The state can't tell: it is the attacker's.
	// PKCE does: Google issued the code for the user's code challenge, and the attacker's browser holds another
	// verifier. The attacker went through Google too, so it's the verifier that fails, not a code Google never issued.
	g, app, user := startGoogleOIDC(t)
	callback := consent(t, user, app)

	attacker := newBrowser(t, app)
	own := consent(t, attacker, app)

	status, body := browse(t, attacker, app.URL+"/callback?code="+url.QueryEscape(callback.Query().Get("code"))+
		"&state="+url.QueryEscape(own.Query().Get("state")))

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "oidcapp: token exchange: invalid_grant\n", body)

	// The attempt didn't spend the code: the user still signs in with it.
	status, body = browse(t, user, callback.String())

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"sub": "110169484474386276334", "email": "octocat@gmail.com", "name": "The Octocat"}`, body)

	g.authorize.AssertNumberOfCalls(t, 2)
	g.token.AssertNumberOfCalls(t, 1)
	g.badGrant.AssertNumberOfCalls(t, 1)
}

func TestGoogleOIDC_RejectsAReusedCode(t *testing.T) {
	// Google codes are single-use: a callback replayed with a code already exchanged must not sign anyone in.
	g, app, browser := startGoogleOIDC(t)

	callback := consent(t, browser, app)
	appURL, err := url.Parse(app.URL)
	require.NoError(t, err)
	cookies := browser.Jar.Cookies(appURL)

	status, _ := browse(t, browser, callback.String())
	require.Equal(t, http.StatusOK, status)

	// The replay brings back the state, the nonce and the code verifier of the sign-in, so only the code being used
	// tells it apart.
	browser.Jar.SetCookies(appURL, cookies)

	status, body := browse(t, browser, callback.String())

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "oidcapp: token exchange: invalid_grant\n", body)

	g.token.AssertNumberOfCalls(t, 1)
	g.badGrant.AssertNumberOfCalls(t, 1)
}

func TestGoogleOIDC_RejectsAnIDTokenItCannotTrust(t *testing.T) {
	// Each case is a token Google would never issue, from a token mock that changes one thing of Google's: the app
	// must refuse each for that one thing, and not sign the user in.
	b64 := base64.RawURLEncoding.EncodeToString

	tests := []struct {
		name string
		// idToken is the template of the ID token. $claims are the claims Google puts in it.
		idToken string
		msg     string
		// jwks is how many times the app fetches the JWKS.
		jwks int64
	}{
		{
			name:    "signed by a key Google doesn't publish",
			idToken: `{{ jwt "rogue" $claims }}`,
			msg:     `oidcapp: id token: unknown kid "rogue"`,
			jwks:    1,
		},
		{
			name: "claims changed after signing",
			idToken: `{{ $signed := split "." (jwt "key-1" $claims) }}` +
				`{{ $changed := split "." (jwt "key-1" (mergePatch "{\"sub\":\"attacker\"}" $claims)) }}` +
				`{{ index $signed 0 }}.{{ index $changed 1 }}.{{ index $signed 2 }}`,
			msg:  "oidcapp: id token: bad signature",
			jwks: 1,
		},
		{
			name:    "unsigned, with alg none",
			idToken: b64([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + b64([]byte(`{"sub":"`+_googleSub+`"}`)) + ".",
			msg:     `oidcapp: id token: alg "none", want RS256`,
		},
		{
			name:    "from another issuer",
			idToken: `{{ jwt "key-1" (mergePatch "{\"iss\":\"https://accounts.google.com\"}" $claims) }}`,
			msg:     "oidcapp: id token: wrong issuer",
			jwks:    1,
		},
		{
			name: "for another app",
			idToken: `{{ jwt "key-1" (mergePatch "{\"aud\":\"other.apps.googleusercontent.com\",` +
				`\"azp\":\"other.apps.googleusercontent.com\"}" $claims) }}`,
			msg:  "oidcapp: id token: wrong audience",
			jwks: 1,
		},
		{
			name: "expired an hour ago",
			idToken: `{{ $exp := printf "{\"exp\":%s}" (now | timeAdd "-1h" | formatTime "unix") }}` +
				`{{ jwt "key-1" (mergePatch $exp $claims) }}`,
			msg:  "oidcapp: id token: expired",
			jwks: 1,
		},
		{
			name:    "for another sign-in",
			idToken: `{{ jwt "key-1" (mergePatch "{\"nonce\":\"replayed\"}" $claims) }}`,
			msg:     "oidcapp: id token: wrong nonce",
			jwks:    1,
		},
		{
			name:    "with an email not verified",
			idToken: `{{ jwt "key-1" (mergePatch "{\"email_verified\":false}" $claims) }}`,
			msg:     "oidcapp: id token: email not verified",
			jwks:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, app, browser := startGoogleOIDC(t)
			untrusted := g.server.MustMock(googleToken(g.callback, tt.idToken).Priority(-1))

			status, body := browse(t, browser, app.URL+"/login")

			require.Equal(t, http.StatusUnauthorized, status)
			require.Equal(t, tt.msg+"\n", body)

			status, body = browse(t, browser, app.URL+"/me")

			require.Equal(t, http.StatusUnauthorized, status)
			require.Equal(t, "oidcapp: sign in first\n", body)

			untrusted.AssertNumberOfCalls(t, 1)
			g.token.AssertNotCalled(t)
			g.jwks.AssertNumberOfCalls(t, tt.jwks)
		})
	}
}

func TestGoogleOIDC_FollowsAKeyRotation(t *testing.T) {
	// Google rotates its signing keys: it publishes the next key before it signs with it. An app that fetched the
	// keys once must fetch them again when a token names a key it doesn't know.
	g, app, browser := startGoogleOIDC(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	rotated := g.server.MustMock(
		mockhttp.Getf("/oauth2/v3/certs").
			Priority(-1).
			Reply(mockhttp.OK().ContentType("application/json").BodyTemplate(`{{ jwks "key-1" "key-2" }}`)),
		googleToken(g.callback, `{{ jwt "key-2" $claims }}`).Priority(-1),
	)

	status, body := browse(t, newBrowser(t, app), app.URL+"/login")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"sub": "110169484474386276334", "email": "octocat@gmail.com", "name": "The Octocat"}`, body)

	g.jwks.AssertNumberOfCalls(t, 1)
	rotated.AssertNumberOfCalls(t, 2) // the new keys, then the token signed with key-2
}

func TestGoogleOIDC_ReadsTheProfileWithTheAccessToken(t *testing.T) {
	g, app, browser := startGoogleOIDC(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	status, body := browse(t, browser, app.URL+"/profile")

	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t,
		`{"sub": "110169484474386276334", "name": "The Octocat", "picture": "https://lh3.googleusercontent.com/a/octocat"}`,
		body)

	g.userinfo.AssertNumberOfCalls(t, 1)
	g.badToken.AssertNotCalled(t)
}

func TestGoogleOIDC_RefusesTheProfileOfAnotherUser(t *testing.T) {
	// Userinfo answers about another user than the ID token's: the answer may have been substituted, and the app must
	// not use it (OpenID Connect Core 5.3.2).
	g, app, browser := startGoogleOIDC(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	other := g.server.MustMock(mockhttp.Getf("/v1/userinfo").
		Priority(-1).
		Reply(mockhttp.OK().JSON(map[string]any{"sub": "100000000000000000001", "name": "Someone Else"})))

	status, body := browse(t, browser, app.URL+"/profile")

	require.Equal(t, http.StatusBadGateway, status)
	require.Equal(t, "oidcapp: userinfo is for another user\n", body)

	other.AssertNumberOfCalls(t, 1)
}

func TestGoogleOIDC_EndsTheSessionWhenGoogleRevokesTheAccessToken(t *testing.T) {
	// The user removes the app's access in their Google account: the access token stops working, and the app must
	// sign the user out rather than keep a session it can't serve.
	g, app, browser := startGoogleOIDC(t)

	status, _ := browse(t, browser, app.URL+"/login")
	require.Equal(t, http.StatusOK, status)

	revoked := g.server.MustMock(mockhttp.Getf("/v1/userinfo").
		Header("Authorization", match.StrictEqual("Bearer "+_googleAccessToken)).
		Priority(-1).
		Reply(mockhttp.Unauthorized().Header("WWW-Authenticate", `Bearer error="invalid_token"`)))

	status, body := browse(t, browser, app.URL+"/profile")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "oidcapp: the provider refused the token, sign in again\n", body)

	status, body = browse(t, browser, app.URL+"/me")

	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "oidcapp: sign in first\n", body)

	revoked.AssertNumberOfCalls(t, 1)
	g.userinfo.AssertNotCalled(t)
}

// startGoogleOIDC starts the Google mocks, the app pointed at them, and a browser.
func startGoogleOIDC(t *testing.T) (*googleMocks, *httptest.Server, *http.Client) {
	t.Helper()

	keys, err := _googleKeys()
	require.NoError(t, err)

	engine := vlatte.MustNewGoTemplateEngine(
		vlatte.WithSigningKey("key-1", "RS256", keys["key-1"]),
		vlatte.WithSigningKey("key-2", "RS256", keys["key-2"]),
		vlatte.WithSigningKey("rogue", "RS256", keys["rogue"]),
	)
	server := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(engine))
	server.MustStart()
	t.Cleanup(server.Close)

	// The app must know its own URL, its redirect URL, before it serves. It serves HTTPS, as its cookies are Secure.
	app := httptest.NewUnstartedServer(nil)
	callback := "https://" + app.Listener.Addr().String() + "/callback"
	app.Config.Handler = oidcapp.New(oidcapp.Config{
		IssuerURL:    server.URL(),
		ClientID:     _googleClientID,
		ClientSecret: _googleClientSecret,
		RedirectURL:  callback,
		Scope:        _googleScope,
	})
	app.StartTLS()
	t.Cleanup(app.Close)

	return mockGoogle(t, server, callback), app, newBrowser(t, app)
}

// googleMocks are the mocks of Google, by what they answer.
type googleMocks struct {
	server   *mockhttp.Server
	callback string
	// discovery answers Google's discovery document, with the server as the issuer.
	discovery *mockhttp.Scope
	// jwks publishes key-1.
	jwks *mockhttp.Scope
	// authorize sends the browser back to the app with a code, as Google does once the user consents.
	authorize *mockhttp.Scope
	// token trades a code and the verifier of its code challenge for an access token and an ID token signed with
	// key-1, once per code.
	token *mockhttp.Scope
	// badGrant answers any other token request, as Google does a code that is wrong, already used or not issued for
	// the code verifier.
	badGrant *mockhttp.Scope
	// userinfo answers the user's claims for the access token.
	userinfo *mockhttp.Scope
	// badToken answers userinfo for any other token or none.
	badToken *mockhttp.Scope
}

// _googleCode sets $code: the code of an authorize request, made from its state and its nonce. It is unique per
// sign-in, made from the request alone, as a scenario key must be, and holds the nonce for the token mock to read
// back, as a provider's opaque code stands for what it recorded.
const _googleCode = `{{ $code := printf "{\"state\":%s,\"nonce\":%s}" (toJSON (.Request.URL.Query.Get "state")) ` +
	`(toJSON (.Request.URL.Query.Get "nonce")) | base64Encode }}`

// _googleClaims sets $claims: the claims of the ID token Google issues for a token request, with the nonce of its
// code and an hour to live.
const _googleClaims = `{{ $nonce := (fromJSON (base64Decode (.Request.PostForm.Get "code"))).nonce }}{{ $now := now }}` +
	`{{ $claims := mergePatch (printf "{\"iss\":%s,\"azp\":%s,\"aud\":%s,\"iat\":%s,\"exp\":%s,\"nonce\":%s}" ` +
	`(toJSON .Server.URL) (toJSON .Extra.client_id) (toJSON .Extra.client_id) ($now | formatTime "unix") ` +
	`($now | timeAdd "1h" | formatTime "unix") (toJSON $nonce)) .Extra.user }}`

// mockGoogle mocks the OpenID Connect endpoints of Google. Each mock matches what Google requires of a request, so one
// the app gets wrong matches none and fails its test.
//
// The scenario instance of a code is keyed by the code challenge it was issued for and the code. The token request's
// key is the S256 of its code verifier and its code, the same only when the verifier fits the challenge: the token
// mock then moves the instance from issued to used, so PKCE and single use are one check.
func mockGoogle(t *testing.T, server *mockhttp.Server, callback string) *googleMocks {
	t.Helper()

	g := &googleMocks{server: server, callback: callback}

	g.discovery = server.MustMock(mockhttp.Getf("/.well-known/openid-configuration").
		Reply(mockhttp.OK().ContentType("application/json").BodyTemplate(`{{ $iss := .Server.URL }}{
"issuer": {{ toJSON $iss }},
"authorization_endpoint": {{ print $iss "/o/oauth2/v2/auth" | toJSON }},
"token_endpoint": {{ print $iss "/token" | toJSON }},
"userinfo_endpoint": {{ print $iss "/v1/userinfo" | toJSON }},
"jwks_uri": {{ print $iss "/oauth2/v3/certs" | toJSON }},
"response_types_supported": ["code", "token", "id_token", "code token", "code id_token", "token id_token",
  "code token id_token", "none"],
"subject_types_supported": ["public"],
"id_token_signing_alg_values_supported": ["RS256"],
"scopes_supported": ["openid", "email", "profile"],
"token_endpoint_auth_methods_supported": ["client_secret_post", "client_secret_basic"],
"code_challenge_methods_supported": ["plain", "S256"],
"grant_types_supported": ["authorization_code", "refresh_token"]}`)))

	g.jwks = server.MustMock(mockhttp.Getf("/oauth2/v3/certs").
		Reply(mockhttp.OK().ContentType("application/json").BodyTemplate(`{{ jwks "key-1" }}`)))

	g.authorize = server.MustMock(mockhttp.Getf("/o/oauth2/v2/auth").
		Query("response_type", match.StrictEqual("code")).
		Query("client_id", match.StrictEqual(_googleClientID)).
		Query("redirect_uri", match.StrictEqual(callback)).
		Query("scope", match.StrictEqual(_googleScope)).
		Query("state", match.Present()).
		Query("nonce", match.Present()).
		Query("code_challenge", match.Present()).
		Query("code_challenge_method", match.StrictEqual("S256")).
		StartScenario(_googleScenario).
		ScenarioKey(_googleCode + `{{ .Request.URL.Query.Get "code_challenge" }}:{{ $code }}`).
		ScenarioStateWillBe("issued").
		Reply(mockhttp.Status(http.StatusFound).HeaderTemplate("Location", _googleCode+
			`{{ .Request.URL.Query.Get "redirect_uri" }}?code={{ urlquery $code }}`+
			`&state={{ .Request.URL.Query.Get "state" | urlquery }}`)))

	g.token = server.MustMock(googleToken(callback, `{{ jwt "key-1" $claims }}`))

	g.badGrant = server.MustMock(mockhttp.Postf("/token").
		Priority(1).
		Reply(mockhttp.BadRequest().JSON(map[string]string{
			"error":             "invalid_grant",
			"error_description": "Bad Request",
		})))

	g.userinfo = server.MustMock(mockhttp.Getf("/v1/userinfo").
		Header("Authorization", match.StrictEqual("Bearer "+_googleAccessToken)).
		Reply(mockhttp.OK().JSON(_googleUser)))

	g.badToken = server.MustMock(mockhttp.Getf("/v1/userinfo").
		Priority(1).
		Reply(mockhttp.Unauthorized().Header("WWW-Authenticate", `Bearer error="invalid_token"`)))

	return g
}

// googleToken is the mock of Google's token endpoint, which answers with the ID token idToken, a template where
// $claims are the claims Google puts in it.
func googleToken(callback, idToken string) *mockhttp.Builder {
	return mockhttp.Postf("/token").
		FormField("grant_type", match.StrictEqual("authorization_code")).
		FormField("client_id", match.StrictEqual(_googleClientID)).
		FormField("client_secret", match.StrictEqual(_googleClientSecret)).
		FormField("redirect_uri", match.StrictEqual(callback)).
		ScenarioIs(_googleScenario).
		ScenarioKey(`{{ hash "sha256" "base64url" (.Request.PostForm.Get "code_verifier") }}:` +
			`{{ .Request.PostForm.Get "code" }}`).
		ScenarioStateIs("issued").
		ScenarioStateWillBe("used").
		Reply(mockhttp.OK().ContentType("application/json").Header("Cache-Control", "no-store").
			BodyTemplate(_googleClaims + `{"access_token": "` + _googleAccessToken + `", "expires_in": 3599, ` +
				`"scope": "openid https://www.googleapis.com/auth/userinfo.email ` +
				`https://www.googleapis.com/auth/userinfo.profile", "token_type": "Bearer", ` +
				`"id_token": "` + idToken + `"}`).
			SetTemplateData(map[string]any{"client_id": _googleClientID, "user": _googleUser}))
}
