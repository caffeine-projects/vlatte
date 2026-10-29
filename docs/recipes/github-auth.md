# GitHub Auth

`recipe/http/githubauth` prepares an HTTP server to stand in for "Sign in with GitHub": the OAuth web flow of an OAuth app, and the profile of the user who signs in. Point the github.com and api.github.com base URLs of the app under test at the server.

```go
import "github.com/caffeine-projects/vlatte/recipe/http/githubauth"

m := mockhttp.MustNew()
err := m.Prep(ctx, githubauth.New(
	githubauth.ClientID("my-app"),
	githubauth.ClientSecret("my-secret"),
	githubauth.Token("gho_test"),
))
```

It covers the OAuth web flow only: not the device flow, nor GitHub Apps.

| Request | Answer |
|---|---|
| `GET /login/oauth/authorize` with the client ID and a `redirect_uri` | 302 to `redirect_uri` with `code` and the request's `state`, as GitHub does once the user consents. Unlike GitHub, `redirect_uri` is required. |
| `POST /login/oauth/access_token` with `Accept: application/json`, and the client ID, the secret and the code in the form | 200 with `access_token`, `token_type` and `scope` as JSON. Without that `Accept`, GitHub answers a form-encoded body: no mock matches. |
| The same with a wrong code | 200 with `error: bad_verification_code`, as GitHub answers. |
| The same with a wrong client ID or secret | 200 with `error: incorrect_client_credentials`. |
| `GET /user` and `GET /user/emails` with `Authorization: Bearer <token>` or `token <token>` | The profile and the addresses. |
| A `GET` of them without the token | 401, `Bad credentials`. |

The code can be exchanged any number of times, unlike GitHub's.

| Option | Sets | Default |
|---|---|---|
| `ClientID(id)`, `ClientSecret(secret)` | The OAuth app's credentials. | `githubauth.DefaultClientID`, `DefaultClientSecret` |
| `Code(code)` | The code the authorize endpoint gives. | `DefaultCode` |
| `Token(token)`, `Scope(scope)` | The token issued, and the scope it says it has. | `DefaultToken`, `DefaultScope` |
| `WithUser(githubauth.User{...})` | The profile of `GET /user`. | octocat |
| `Emails(githubauth.Email{...})` | The addresses of `GET /user/emails`. | The profile's email, primary and verified, or none |

Mock the rest of the API the app calls, such as `GET /user/repos`, as any other mock: the recipe's 401 answers `/user` and `/user/emails` only.
