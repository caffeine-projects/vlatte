# OpenID Connect

`recipe/http/oidc` prepares an HTTP server to stand in for an OpenID Connect provider, for "Sign in with ..." through the authorization code flow: discovery, a JWKS, ID tokens the server signs, PKCE and userinfo. Point the issuer URL of the app under test at the server.

The server signs ID tokens with a key of its own, which the recipe can't add: give it one first, with the setting [`template.keys`](/templating/#tokens).

```go
import "github.com/caffeine-projects/vlatte/recipe/http/oidc"

m := mockhttp.MustNew(mockhttp.Setup().TemplateKeys(oidc.DefaultKeyID + "=RS256"))
err := m.Prep(ctx, oidc.New(
	oidc.ClientID("my-app"),
	oidc.ClientSecret("my-secret"),
	oidc.Claims(map[string]any{"sub": "ana", "email": "ana@example.com", "email_verified": true}),
))
```

A server without the key fails `Prep` with an error naming it. In Go, a key of your own goes on the engine with `vlatte.WithSigningKey`; name its kid with `oidc.KeyID(kid)` when it isn't `oidc.DefaultKeyID`.

| Request | Answer |
|---|---|
| `GET /.well-known/openid-configuration` | The issuer, the endpoints below on it, and what the provider supports: the code flow, `client_secret_post`, PKCE S256 and the key's algorithm. |
| `GET /jwks` | The public half of the signing key. |
| `GET /authorize` with `response_type=code`, the client ID, a `redirect_uri` and a `scope` with `openid` | 302 to `redirect_uri` with a new `code` and the request's `state`. The `nonce`, `code_challenge` and `redirect_uri` are kept with the code. |
| The same with a `code_challenge` but no `code_challenge_method=S256` | 302 to `redirect_uri` with `error=invalid_request` and the request's `state`. |
| `POST /token` with `grant_type=authorization_code`, the client ID and secret, a `redirect_uri` and a code it issued | `access_token`, `token_type`, `expires_in` and an `id_token` signed with the server's key: the user's claims, `iss`, `aud`, `iat`, `exp`, and the `nonce` when the authorize request sent one. |
| The same with a code already exchanged, unknown, issued for another `redirect_uri`, or whose `code_verifier` doesn't fit its `code_challenge` | 400, `invalid_grant`. A code issued with a challenge needs its verifier. |
| `GET /userinfo` with `Authorization: Bearer <access token>` | The user's claims. |
| Any other `GET /userinfo` | 401 with `WWW-Authenticate: Bearer error="invalid_token"`. |

Each code is exchanged once, and PKCE takes S256 only. Every mock is a template, which the admin API shows: codes, PKCE, the `redirect_uri` and single use are a [keyed scenario](/scenarios/#keys) named `oidc-code`, and tokens come from the `jwt` and `jwks` [template functions](/templating/#tokens).

| Option | Sets | Default |
|---|---|---|
| `ClientID(id)`, `ClientSecret(secret)` | The app's credentials. ID tokens have the client ID as `aud`. | `oidc.DefaultClientID`, `DefaultClientSecret` |
| `KeyID(kid)` | The server's key ID tokens are signed with. Discovery announces its algorithm. | `DefaultKeyID` |
| `Issuer(url)` | The `iss` of ID tokens, and the base URL of the endpoints. | The server's URL |
| `Claims(map)` | The user's claims, in ID tokens and from userinfo. | `oidc.DefaultClaims()` |
| `AccessToken(token)`, `TokenTTL(d)` | The access token issued, and how long tokens last. | `DefaultAccessToken`, `DefaultTokenTTL` (1h) |
| `WithPaths(oidc.Paths{...})` | Where the endpoints are, such as Google's `/o/oauth2/v2/auth`. | `oidc.DefaultPaths()` |

It covers the authorization code flow only: not the implicit or hybrid flows, refresh tokens, nor `client_secret_basic`.
