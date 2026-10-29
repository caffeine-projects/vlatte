// Package githubauth provides a recipe that prepares an HTTP mock server to stand in for "Sign in with GitHub": the
// OAuth web flow of an OAuth app, and the profile of the user who signs in. Point the github.com and api.github.com
// base URLs of the app under test at the mock server.
//
//	err := m.Prep(ctx, githubauth.New(githubauth.ClientID("my-app"), githubauth.Token("gho_test")))
//
// The recipe covers the OAuth web flow only: not the device flow, nor GitHub Apps.
package githubauth

import (
	"context"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// Defaults of the recipe, for a test to send what the recipe expects without setting it.
const (
	DefaultClientID     = "vlatte-client-id"
	DefaultClientSecret = "vlatte-client-secret"
	DefaultCode         = "vlatte-code"
	DefaultToken        = "gho_vlatte"
	DefaultScope        = "read:user,user:email"
)

// The paths the recipe mocks, the first two of github.com, the others of api.github.com.
const (
	AuthorizePath   = "/login/oauth/authorize"
	AccessTokenPath = "/login/oauth/access_token"
	UserPath        = "/user"
	UserEmailsPath  = "/user/emails"
)

// User is the profile GET /user answers, with the fields an app signing users in usually reads.
type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	Type      string `json:"type"`
}

// Email is an address GET /user/emails answers.
type Email struct {
	Email      string `json:"email"`
	Primary    bool   `json:"primary"`
	Verified   bool   `json:"verified"`
	Visibility string `json:"visibility"`
}

// Option configures the recipe [New] returns.
type Option func(c *config)

type config struct {
	clientID     string
	clientSecret string
	code         string
	token        string
	scope        string
	user         User
	emails       []Email
}

// ClientID sets the client ID of the OAuth app: the authorize and access token endpoints answer only requests that
// send it. The default is [DefaultClientID].
func ClientID(id string) Option {
	return func(c *config) { c.clientID = id }
}

// ClientSecret sets the client secret of the OAuth app, which the access token endpoint asks for. The default is
// [DefaultClientSecret].
func ClientSecret(secret string) Option {
	return func(c *config) { c.clientSecret = secret }
}

// Code sets the code the authorize endpoint gives the app, and the only one the access token endpoint exchanges.
// Unlike GitHub's, it can be exchanged any number of times. The default is [DefaultCode].
func Code(code string) Option {
	return func(c *config) { c.code = code }
}

// Token sets the access token the access token endpoint issues, and the only one GET /user and GET /user/emails
// accept. The default is [DefaultToken].
func Token(token string) Option {
	return func(c *config) { c.token = token }
}

// Scope sets the scope the access token endpoint says the token has. The default is [DefaultScope].
func Scope(scope string) Option {
	return func(c *config) { c.scope = scope }
}

// WithUser sets the profile GET /user answers. The default is the user octocat. It is not named User, the type of
// the profile.
func WithUser(user User) Option {
	return func(c *config) { c.user = user }
}

// Emails sets the addresses GET /user/emails answers. The default is the email of the profile, primary and verified,
// or none when the profile has no email.
func Emails(emails ...Email) Option {
	return func(c *config) { c.emails = emails }
}

// New returns a recipe that mocks the OAuth web flow of GitHub and the profile of the user who signs in:
//
//   - GET /login/oauth/authorize, with the client ID and a redirect_uri, redirects to redirect_uri with the code and
//     the request's state, as GitHub does once the user consents. Unlike GitHub, which falls back to the callback URL
//     the app registered, it needs redirect_uri: without it, no mock matches.
//   - POST /login/oauth/access_token, with Accept: application/json and the client ID and secret and the code in its
//     form, answers the token as JSON. A wrong code or wrong credentials get GitHub's error answer: a 200 whose JSON
//     has error set to bad_verification_code or incorrect_client_credentials. GitHub answers a form-encoded body
//     without that Accept; the recipe matches no such request.
//   - GET /user and GET /user/emails, with the token in an Authorization header, as "Bearer <token>" or
//     "token <token>", answer the profile and the addresses. Any other GET to them gets a 401, Bad credentials.
func New(opts ...Option) mockhttp.Recipe {
	c := &config{
		clientID:     DefaultClientID,
		clientSecret: DefaultClientSecret,
		code:         DefaultCode,
		token:        DefaultToken,
		scope:        DefaultScope,
		user: User{
			ID:        1,
			Login:     "octocat",
			Name:      "The Octocat",
			Email:     "octocat@github.com",
			AvatarURL: "https://github.com/images/error/octocat_happy.gif",
			HTMLURL:   "https://github.com/octocat",
			Type:      "User",
		},
	}
	for _, opt := range opts {
		opt(c)
	}

	if c.emails == nil {
		c.emails = []Email{}
		if c.user.Email != "" {
			c.emails = append(c.emails, Email{Email: c.user.Email, Primary: true, Verified: true, Visibility: "public"})
		}
	}

	return vlatte.RecipeFunc[*mockhttp.Mock, *mockhttp.Server](func(
		_ context.Context,
		in *vlatte.RecipeInput[*mockhttp.Mock, *mockhttp.Server],
	) error {
		_, err := in.Server.Mock(c.mocks()...)
		return err
	})
}

// _redirect is the Location of the authorize endpoint's answer: redirect_uri with the code and the request's state.
const _redirect = `{{ $u := .Request.URL.Query.Get "redirect_uri" }}{{ $u }}{{ if contains "?" $u }}&{{ else }}?{{ end }}` +
	`code={{ urlquery .Extra.code }}{{ with .Request.URL.Query.Get "state" }}&state={{ urlquery . }}{{ end }}`

const _docs = "https://docs.github.com/apps/oauth-apps/maintaining-oauth-apps/troubleshooting-oauth-app-access-token-request-errors"

func (c *config) mocks() []vlatte.Builder[*mockhttp.Mock, *mockhttp.Server] {
	acceptJSON := match.StrictEqual("application/json")
	authorized := match.Either(match.StrictEqual("Bearer "+c.token), match.StrictEqual("token "+c.token))
	badCredentials := func() *mockhttp.Reply {
		return mockhttp.Unauthorized().JSON(map[string]string{
			"message":           "Bad credentials",
			"documentation_url": "https://docs.github.com/rest",
			"status":            "401",
		})
	}

	return []vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]{
		mockhttp.Getf(AuthorizePath).
			Query("client_id", match.StrictEqual(c.clientID)).
			Query("redirect_uri", match.Present()).
			Reply(mockhttp.Status(http.StatusFound).
				HeaderTemplate("Location", _redirect).
				SetTemplateData(map[string]string{"code": c.code})),

		mockhttp.Postf(AccessTokenPath).
			Header("Accept", acceptJSON).
			FormField("client_id", match.StrictEqual(c.clientID)).
			FormField("client_secret", match.StrictEqual(c.clientSecret)).
			FormField("code", match.StrictEqual(c.code)).
			Reply(mockhttp.OK().JSON(map[string]string{
				"access_token": c.token,
				"token_type":   "bearer",
				"scope":        c.scope,
			})),
		mockhttp.Postf(AccessTokenPath).
			Priority(1).
			Header("Accept", acceptJSON).
			FormField("client_id", match.StrictEqual(c.clientID)).
			FormField("client_secret", match.StrictEqual(c.clientSecret)).
			Reply(mockhttp.OK().JSON(map[string]string{
				"error":             "bad_verification_code",
				"error_description": "The code passed is incorrect or expired.",
				"error_uri":         _docs + "#bad-verification-code",
			})),
		mockhttp.Postf(AccessTokenPath).
			Priority(2).
			Header("Accept", acceptJSON).
			Reply(mockhttp.OK().JSON(map[string]string{
				"error":             "incorrect_client_credentials",
				"error_description": "The client_id and/or client_secret passed are incorrect.",
				"error_uri":         _docs + "#incorrect-client-credentials",
			})),

		mockhttp.Getf(UserPath).Header("Authorization", authorized).Reply(mockhttp.OK().JSON(c.user)),
		mockhttp.Getf(UserPath).Priority(1).Reply(badCredentials()),
		mockhttp.Getf(UserEmailsPath).Header("Authorization", authorized).Reply(mockhttp.OK().JSON(c.emails)),
		mockhttp.Getf(UserEmailsPath).Priority(1).Reply(badCredentials()),
	}
}
