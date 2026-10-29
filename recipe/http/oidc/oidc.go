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
	"maps"
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

// KeyID sets the kid of the server's key that ID tokens are signed with. Discovery announces the key's algorithm. The
// default is [DefaultKeyID]. A server without the key fails Prep.
func KeyID(kid string) Option {
	return func(c *config) { c.kid = kid }
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
// iat, exp and nonce), and those userinfo answers. The default is [DefaultClaims]. The recipe keeps a copy: changing
// claims afterwards changes nothing.
func Claims(claims map[string]any) Option {
	return func(c *config) { c.claims = maps.Clone(claims) }
}

// WithPaths sets where the endpoints are; the fields left empty keep their default.
func WithPaths(paths Paths) Option {
	return func(c *config) { c.paths = paths }
}

// New returns a recipe that mocks an OpenID Connect provider:
//
//   - GET discovery answers the provider's metadata: the issuer, the endpoints below on it, and what it supports.
//   - GET jwks answers the public half of the signing key.
//   - GET authorize, with response_type=code, the client ID, a redirect_uri and a scope with openid, redirects to
//     redirect_uri with a new code and the request's state, as a provider does once the user consents. It records
//     the request's nonce, PKCE code_challenge and redirect_uri with the code. PKCE takes S256 only: a code_challenge
//     without code_challenge_method=S256 redirects to redirect_uri with error=invalid_request instead.
//   - POST token, with grant_type=authorization_code, the client ID and secret, a redirect_uri and a code it issued,
//     answers an access token and an ID token signed with the server's key, holding the user's claims and the nonce.
//     Each code is exchanged once, with the redirect_uri it was issued for. When the authorize request sent a
//     code_challenge, the code_verifier must be one whose S256 is it. Any other exchange gets 400 invalid_grant.
//   - GET userinfo, with the access token as "Bearer <token>", answers the user's claims. Any other GET to it gets a
//     401 with WWW-Authenticate: Bearer error="invalid_token".
func New(opts ...Option) mockhttp.Recipe {
	c := &config{
		clientID:     DefaultClientID,
		clientSecret: DefaultClientSecret,
		kid:          DefaultKeyID,
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
// its code and its redirect_uri.
const _authorizeKey = _code + `{{ .Request.URL.Query.Get "code_challenge" }}:{{ $code }}:` +
	`{{ .Request.URL.Query.Get "redirect_uri" }}`

// _tokenKey is the key of the scenario instance of a token request: the S256 of its code_verifier, empty without one,
// its code and its redirect_uri. It is the key of the authorize request that issued the code only when the verifier
// fits the challenge and the redirect_uri is the same (RFC 6749 §4.1.3), so PKCE, the redirect_uri and single use are
// one check.
const _tokenKey = `{{ with .Request.PostForm.Get "code_verifier" }}{{ hash "sha256" "base64url" . }}{{ end }}:` +
	`{{ .Request.PostForm.Get "code" }}:{{ .Request.PostForm.Get "redirect_uri" }}`

// _redirect is the Location of the authorize endpoint's answer: redirect_uri with the code and the request's state.
const _redirect = _code + `{{ $u := .Request.URL.Query.Get "redirect_uri" }}{{ $u }}` +
	`{{ if contains "?" $u }}&{{ else }}?{{ end }}code={{ urlquery $code }}` +
	`{{ with .Request.URL.Query.Get "state" }}&state={{ urlquery . }}{{ end }}`

// _refusal is the Location of the authorize endpoint's answer to a code_challenge whose method is not S256: redirect_uri
// with an invalid_request error and the request's state (RFC 6749 §4.1.2.1).
const _refusal = `{{ $u := .Request.URL.Query.Get "redirect_uri" }}{{ $u }}` +
	`{{ if contains "?" $u }}&{{ else }}?{{ end }}error=invalid_request` +
	`&error_description={{ urlquery "PKCE takes S256 only: send code_challenge_method=S256" }}` +
	`{{ with .Request.URL.Query.Get "state" }}&state={{ urlquery . }}{{ end }}`

// discovery is the body of the discovery endpoint, which reads the algorithm of the signing key kid from its JWK.
func discovery(kid string) string {
	return `{{ $iss := or .Extra.issuer .Server.URL }}{"issuer": {{ toJSON $iss }},
"authorization_endpoint": {{ print $iss .Extra.paths.authorize | toJSON }},
"token_endpoint": {{ print $iss .Extra.paths.token | toJSON }},
"jwks_uri": {{ print $iss .Extra.paths.jwks | toJSON }},
"userinfo_endpoint": {{ print $iss .Extra.paths.userinfo | toJSON }},
"response_types_supported": ["code"],
"grant_types_supported": ["authorization_code"],
"subject_types_supported": ["public"],
"id_token_signing_alg_values_supported": [{{ (index (fromJSON (jwks ` + strconv.Quote(kid) + `)).keys 0).alg | toJSON }}],
"scopes_supported": ["openid", "email", "profile"],
"token_endpoint_auth_methods_supported": ["client_secret_post"],
"code_challenge_methods_supported": ["S256"]}`
}

// authorize starts a mock of the authorize requests of a sign-in with the client of c.
func authorize(c *config) *mockhttp.Builder {
	return endpoint(http.MethodGet, c.paths.Authorize).
		Query("response_type", match.StrictEqual("code")).
		Query("client_id", match.StrictEqual(c.clientID)).
		Query("redirect_uri", match.Present()).
		Query("scope", match.Split(" ", match.Contain("openid")))
}

// endpoint starts a mock of requests of method to path, exactly.
func endpoint(method, path string) *mockhttp.Builder {
	return mockhttp.Request().Method(method).URLPath(match.StrictEqual(path))
}

func (c *config) mocks() []vlatte.Builder[*mockhttp.Mock, *mockhttp.Server] {
	data := map[string]any{
		"issuer":       c.issuer,
		"client_id":    c.clientID,
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
			Reply(mockhttp.OK().ContentType(mockhttp.MIMEApplicationJSON).BodyTemplate(discovery(c.kid)).SetTemplateData(data)),

		endpoint(http.MethodGet, c.paths.JWKS).
			Reply(mockhttp.OK().ContentType(mockhttp.MIMEApplicationJSON).BodyTemplate(`{{ jwks ` + strconv.Quote(c.kid) + ` }}`)),

		authorize(c).
			StartScenario(Scenario).
			ScenarioKey(_authorizeKey).
			ScenarioStateWillBe("issued").
			Reply(mockhttp.Status(http.StatusFound).HeaderTemplate(mockhttp.HeaderLocation, _redirect)),
		// A challenge without a method is plain (RFC 7636 §4.3).
		authorize(c).
			Query("code_challenge", match.Present()).
			Query("code_challenge_method", match.Not(match.StrictEqual("S256"))).
			Priority(-1).
			Reply(mockhttp.Status(http.StatusFound).HeaderTemplate(mockhttp.HeaderLocation, _refusal)),

		endpoint(http.MethodPost, c.paths.Token).
			FormField("grant_type", match.StrictEqual("authorization_code")).
			FormField("client_id", match.StrictEqual(c.clientID)).
			FormField("client_secret", match.StrictEqual(c.clientSecret)).
			FormField("redirect_uri", match.Present()).
			ScenarioIs(Scenario).
			ScenarioKey(_tokenKey).
			ScenarioStateIs("issued").
			ScenarioStateWillBe("used").
			Reply(mockhttp.OK().ContentType(mockhttp.MIMEApplicationJSON).Header(mockhttp.HeaderCacheControl, "no-store").
				BodyTemplate(token).SetTemplateData(data)),
		endpoint(http.MethodPost, c.paths.Token).
			Priority(1).
			Reply(mockhttp.BadRequest().JSON(map[string]string{
				"error":             "invalid_grant",
				"error_description": "the code is unknown, used, or doesn't fit the code_verifier",
			})),

		endpoint(http.MethodGet, c.paths.UserInfo).
			Header(mockhttp.HeaderAuthorization, match.StrictEqual("Bearer "+c.accessToken)).
			Reply(mockhttp.OK().JSON(c.claims)),
		endpoint(http.MethodGet, c.paths.UserInfo).
			Priority(1).
			Reply(mockhttp.Unauthorized().Header(mockhttp.HeaderWWWAuthenticate, `Bearer error="invalid_token"`)),
	}
}
