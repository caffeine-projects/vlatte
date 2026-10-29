// Package oidc provides a recipe that prepares an HTTP mock server to stand in for an OpenID Connect provider: "Sign
// in with ..." through the authorization code flow, with discovery, a JWKS, ID tokens the server signs, PKCE and
// userinfo. Point the issuer URL of the app under test at the mock server.
//
// The server signs with a key of its own, which the recipe can't add: give it one first, with the setting
// template.keys or, in Go, vlatte.WithSigningKey on its template engine.
//
//	m := mockhttp.MustNew(mockhttp.Setup().TemplateKeys(oidc.DefaultKeyID + "=RS256"))
//	err := m.Prep(ctx, oidc.New(oidc.ClientID("my-app")))
//
// Every mock the recipe adds is a template, which the admin API shows and mock files could hold: it uses the jwt
// and jwks template functions, keyed scenarios and no Go code.
package oidc

import (
	"cmp"
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// Defaults of the recipe, for a test to send what the recipe expects without setting it.
const (
	DefaultClientID     = "vlatte-client-id"
	DefaultClientSecret = "vlatte-client-secret"
	DefaultKeyID        = "vlatte"
	DefaultAlg          = "RS256"
	DefaultAccessToken  = "vlatte-access-token"
	DefaultTokenTTL     = time.Hour
)

// Scenario is the scenario of the codes the authorize endpoint issues: each code is an instance of its own, issued,
// then used once exchanged.
const Scenario = "oidc-code"

// Paths are where the provider serves its endpoints, from its issuer URL. An empty one is its default.
type Paths struct {
	Discovery string // default /.well-known/openid-configuration, which OpenID Connect Discovery fixes
	Authorize string // default /authorize
	Token     string // default /token
	JWKS      string // default /jwks
	UserInfo  string // default /userinfo
}

// DefaultPaths returns the paths of the recipe without [WithPaths].
func DefaultPaths() Paths {
	return Paths{
		Discovery: "/.well-known/openid-configuration",
		Authorize: "/authorize",
		Token:     "/token",
		JWKS:      "/jwks",
		UserInfo:  "/userinfo",
	}
}

// DefaultClaims returns the claims of the user who signs in without [Claims].
func DefaultClaims() map[string]any {
	return map[string]any{
		"sub":            "vlatte-user",
		"email":          "user@vlatte.example",
		"email_verified": true,
		"name":           "VLatte User",
	}
}

// Option configures the recipe [New] returns.
type Option func(c *config)

type config struct {
	clientID     string
	clientSecret string
	kid          string
	alg          string
	issuer       string
	accessToken  string
	ttl          time.Duration
	claims       map[string]any
	paths        Paths
}

// ClientID sets the client ID of the app: the authorize and token endpoints answer only requests that send it, and
// ID tokens have it as aud. The default is [DefaultClientID].
func ClientID(id string) Option {
	return func(c *config) { c.clientID = id }
}

// ClientSecret sets the secret the token endpoint asks for, in the form (client_secret_post). The default is
// [DefaultClientSecret].
func ClientSecret(secret string) Option {
	return func(c *config) { c.clientSecret = secret }
}

// SigningKey sets the key of the server ID tokens are signed with, and its algorithm, which discovery announces. The
// defaults are [DefaultKeyID] and [DefaultAlg]. A server without the key fails Prep.
func SigningKey(kid, alg string) Option {
	return func(c *config) { c.kid, c.alg = kid, alg }
}

// Issuer sets the issuer: the iss of ID tokens, and the base URL of the endpoints discovery announces. The default
// is the URL of the server, so the recipe works on any port; set it when the app reaches the server by another name.
func Issuer(issuer string) Option {
	return func(c *config) { c.issuer = issuer }
}

// AccessToken sets the access token the token endpoint issues, and the only one userinfo accepts. The default is
// [DefaultAccessToken].
func AccessToken(token string) Option {
	return func(c *config) { c.accessToken = token }
}

// TokenTTL sets how long ID and access tokens last: the exp of ID tokens and the expires_in of the token answer. The
// default is [DefaultTokenTTL].
func TokenTTL(ttl time.Duration) Option {
	return func(c *config) { c.ttl = ttl }
}

// Claims sets the claims of the user who signs in: those of ID tokens, but the ones the provider sets (iss, aud,
// iat, exp and nonce), and those userinfo answers. The default is [DefaultClaims].
func Claims(claims map[string]any) Option {
	return func(c *config) { c.claims = claims }
}

// WithPaths sets where the endpoints are; the fields left empty keep their default. It is not named Paths, the type
// of the paths.
func WithPaths(paths Paths) Option {
	return func(c *config) { c.paths = paths }
}

// New returns a recipe that mocks an OpenID Connect provider:
//
//   - GET discovery answers the provider's metadata: the issuer, the endpoints below on it, and what it supports.
//   - GET jwks answers the public half of the signing key.
//   - GET authorize, with response_type=code, the client ID, a redirect_uri and a scope with openid, redirects to
//     redirect_uri with a new code and the request's state, as a provider does once the user consents. It records
//     the request's nonce and PKCE code_challenge with the code.
//   - POST token, with grant_type=authorization_code, the client ID and secret, a redirect_uri and a code it issued,
//     answers an access token and an ID token signed with the server's key, holding the user's claims and the nonce.
//     Each code is exchanged once. When the authorize request sent a code_challenge, the code_verifier must be one
//     whose S256 is it; PKCE takes S256 only. Any other exchange gets 400 invalid_grant.
//   - GET userinfo, with the access token as "Bearer <token>", answers the user's claims. Any other GET to it gets a
//     401 with WWW-Authenticate: Bearer error="invalid_token".
func New(opts ...Option) mockhttp.Recipe {
	c := &config{
		clientID:     DefaultClientID,
		clientSecret: DefaultClientSecret,
		kid:          DefaultKeyID,
		alg:          DefaultAlg,
		accessToken:  DefaultAccessToken,
		ttl:          DefaultTokenTTL,
		claims:       DefaultClaims(),
	}
	for _, opt := range opts {
		opt(c)
	}

	defaults := DefaultPaths()
	c.paths = Paths{
		Discovery: cmp.Or(c.paths.Discovery, defaults.Discovery),
		Authorize: cmp.Or(c.paths.Authorize, defaults.Authorize),
		Token:     cmp.Or(c.paths.Token, defaults.Token),
		JWKS:      cmp.Or(c.paths.JWKS, defaults.JWKS),
		UserInfo:  cmp.Or(c.paths.UserInfo, defaults.UserInfo),
	}

	return vlatte.RecipeFunc[*mockhttp.Mock, *mockhttp.Server](func(
		_ context.Context,
		in *vlatte.RecipeInput[*mockhttp.Mock, *mockhttp.Server],
	) error {
		_, err := in.Server.Mock(c.mocks()...)
		return err
	})
}

// _code sets $code: the code of an authorize request. It is made from the request alone, as a scenario key must be,
// unique by the request's ID, and holds the nonce for the token endpoint to read back, as a provider's opaque code
// stands for what it recorded.
const _code = `{{ $code := printf "{\"id\":%s,\"nonce\":%s}" (toJSON .Request.ID) ` +
	`(toJSON (.Request.URL.Query.Get "nonce")) | base64Encode }}`

// _authorizeKey is the key of the scenario instance of an authorize request: its code_challenge, empty without one,
// and its code.
const _authorizeKey = _code + `{{ .Request.URL.Query.Get "code_challenge" }}:{{ $code }}`

// _tokenKey is the key of the scenario instance of a token request: the S256 of its code_verifier, empty without one,
// and its code. It is the key of the authorize request that issued the code only when the verifier fits the
// challenge, so PKCE and single use are one check.
const _tokenKey = `{{ with .Request.PostForm.Get "code_verifier" }}{{ hash "sha256" "base64url" . }}{{ end }}:` +
	`{{ .Request.PostForm.Get "code" }}`

// _redirect is the Location of the authorize endpoint's answer: redirect_uri with the code and the request's state.
const _redirect = _code + `{{ $u := .Request.URL.Query.Get "redirect_uri" }}{{ $u }}` +
	`{{ if contains "?" $u }}&{{ else }}?{{ end }}code={{ urlquery $code }}` +
	`{{ with .Request.URL.Query.Get "state" }}&state={{ urlquery . }}{{ end }}`

// _discovery is the body of the discovery endpoint.
const _discovery = `{{ $iss := or .Extra.issuer .Server.URL }}{"issuer": {{ toJSON $iss }},
"authorization_endpoint": {{ print $iss .Extra.paths.authorize | toJSON }},
"token_endpoint": {{ print $iss .Extra.paths.token | toJSON }},
"jwks_uri": {{ print $iss .Extra.paths.jwks | toJSON }},
"userinfo_endpoint": {{ print $iss .Extra.paths.userinfo | toJSON }},
"response_types_supported": ["code"],
"grant_types_supported": ["authorization_code"],
"subject_types_supported": ["public"],
"id_token_signing_alg_values_supported": [{{ toJSON .Extra.alg }}],
"scopes_supported": ["openid", "email", "profile"],
"token_endpoint_auth_methods_supported": ["client_secret_post"],
"code_challenge_methods_supported": ["S256"]}`

// endpoint starts a mock of requests of method to path, exactly.
func endpoint(method, path string) *mockhttp.Builder {
	return mockhttp.Request().Method(method).URLPath(match.StrictEqual(path))
}

func (c *config) mocks() []vlatte.Builder[*mockhttp.Mock, *mockhttp.Server] {
	data := map[string]any{
		"issuer":       c.issuer,
		"client_id":    c.clientID,
		"alg":          c.alg,
		"access_token": c.accessToken,
		"ttl":          c.ttl.String(),
		"expires_in":   int64(c.ttl / time.Second),
		"claims":       c.claims,
		"paths": map[string]string{
			"authorize": c.paths.Authorize,
			"token":     c.paths.Token,
			"jwks":      c.paths.JWKS,
			"userinfo":  c.paths.UserInfo,
		},
	}

	// The provider's claims go on the user's, and a nonce of null, when the authorize request sent none, removes it.
	token := `{{ $code := fromJSON (base64Decode (.Request.PostForm.Get "code")) }}` +
		`{{ $nonce := "null" }}{{ with $code.nonce }}{{ $nonce = toJSON . }}{{ end }}` +
		`{{ $now := now }}` +
		`{{ $claims := mergePatch (printf "{\"iss\":%s,\"aud\":%s,\"iat\":%s,\"exp\":%s,\"nonce\":%s}" ` +
		`(toJSON (or .Extra.issuer .Server.URL)) (toJSON .Extra.client_id) ($now | formatTime "unix") ` +
		`($now | timeAdd .Extra.ttl | formatTime "unix") $nonce) .Extra.claims }}` +
		`{"access_token": {{ toJSON .Extra.access_token }}, "token_type": "Bearer", ` +
		`"expires_in": {{ .Extra.expires_in }}, "scope": "openid", ` +
		`"id_token": "{{ jwt ` + strconv.Quote(c.kid) + ` $claims }}"}`

	return []vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]{
		endpoint(http.MethodGet, c.paths.Discovery).
			Reply(mockhttp.OK().ContentType("application/json").BodyTemplate(_discovery).SetTemplateData(data)),

		endpoint(http.MethodGet, c.paths.JWKS).
			Reply(mockhttp.OK().ContentType("application/json").BodyTemplate(`{{ jwks ` + strconv.Quote(c.kid) + ` }}`)),

		endpoint(http.MethodGet, c.paths.Authorize).
			Query("response_type", match.StrictEqual("code")).
			Query("client_id", match.StrictEqual(c.clientID)).
			Query("redirect_uri", match.Present()).
			Query("scope", match.Split(" ", match.Contain("openid"))).
			StartScenario(Scenario).
			ScenarioKey(_authorizeKey).
			ScenarioStateWillBe("issued").
			Reply(mockhttp.Status(http.StatusFound).HeaderTemplate("Location", _redirect)),

		endpoint(http.MethodPost, c.paths.Token).
			FormField("grant_type", match.StrictEqual("authorization_code")).
			FormField("client_id", match.StrictEqual(c.clientID)).
			FormField("client_secret", match.StrictEqual(c.clientSecret)).
			FormField("redirect_uri", match.Present()).
			ScenarioIs(Scenario).
			ScenarioKey(_tokenKey).
			ScenarioStateIs("issued").
			ScenarioStateWillBe("used").
			Reply(mockhttp.OK().ContentType("application/json").Header("Cache-Control", "no-store").
				BodyTemplate(token).SetTemplateData(data)),
		endpoint(http.MethodPost, c.paths.Token).
			Priority(1).
			Reply(mockhttp.BadRequest().JSON(map[string]string{
				"error":             "invalid_grant",
				"error_description": "the code is unknown, used, or doesn't fit the code_verifier",
			})),

		endpoint(http.MethodGet, c.paths.UserInfo).
			Header("Authorization", match.StrictEqual("Bearer "+c.accessToken)).
			Reply(mockhttp.OK().JSON(c.claims)),
		endpoint(http.MethodGet, c.paths.UserInfo).
			Priority(1).
			Reply(mockhttp.Unauthorized().Header("WWW-Authenticate", `Bearer error="invalid_token"`)),
	}
}
