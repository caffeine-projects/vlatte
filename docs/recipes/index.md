# Recipes

A recipe prepares a server, for example by adding a set of mocks. `m.Prep(ctx, recipes...)` runs recipes in order, and stops at the first that fails. Recipes live under `recipe/<protocol>/<recipe>`.

```go
m := mockhttp.MustNew()
err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
```

| Recipe | Prepares |
|---|---|
| [Proxy all](/recipes/proxy-all) | A mock that sends what no other mock matches to a real service. |
| [GitHub Auth](/recipes/github-auth) | "Sign in with GitHub": the OAuth web flow and the user's profile. |
| [OpenID Connect](/recipes/oidc) | An OpenID Connect provider: discovery, JWKS, signed ID tokens, PKCE and userinfo. |

## From the vlatte command

The `vlatte` command prepares the built-in GitHub Auth and OpenID Connect recipes from its settings: no Go code. Setting a key of the `recipe.github_auth` or `recipe.oidc` section turns the recipe on, and `--recipe-oidc=false` turns it off. Only built-in recipes are available; Proxy all is the `proxy_all` setting.

| Section | Keys |
|---|---|
| `recipe.github_auth` | `client_id`, `client_secret`, `code`, `token`, `scope`, `user.id`, `user.login`, `user.name`, `user.email`, `user.avatar_url`, `user.html_url`, `user.type`, `emails` |
| `recipe.oidc` | `client_id`, `client_secret`, `key_id`, `issuer`, `access_token`, `token_ttl`, `claims`, `paths.discovery`, `paths.authorize`, `paths.token`, `paths.jwks`, `paths.userinfo` |

Each key is set in three ways, as every other setting is: in the config file as `recipe.oidc.client_id`, with the flag `--recipe-oidc-client-id` and with the variable `VLATTE_RECIPE_OIDC_CLIENT_ID`. A list or an object, such as `emails` and `claims`, is a list or an object in the file, and its JSON text in a flag or a variable.

```sh
vlatte --template-keys vlatte=RS256 --recipe-oidc --recipe-oidc-claims '{"sub":"ana"}'
```

A mistyped key, flag or variable is an error with a suggestion. The recipes' mocks are added in memory at every start: the admin API's store doesn't keep them. `vlatte --help` lists every key.

A recipe of your own takes a few lines: see [Writing a recipe](/recipes/writing).
