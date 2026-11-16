package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/httpx"
	"github.com/caffeine-projects/vlatte/internal/tmplfunc"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/recipe/http/githubauth"
	"github.com/caffeine-projects/vlatte/recipe/http/oidc"
)

// recipes are the built-in recipes the recipe section of the settings turns on, for the HTTP server.
type recipes struct {
	githubAuth *githubAuthRecipe // recipe.github_auth; nil when off
	oidc       *oidcRecipe       // recipe.oidc; nil when off
}

// githubAuthRecipe is the recipe.github_auth section: the options of the githubauth recipe its keys set.
type githubAuthRecipe struct {
	opts []githubauth.Option
	user githubauth.User // from the user.* keys, starting at githubauth.DefaultUser()
}

// oidcRecipe is the recipe.oidc section: the options of the oidc recipe its keys set.
type oidcRecipe struct {
	opts   []oidc.Option
	paths  oidc.Paths // from the paths.* keys; an empty one keeps its default
	kid    string     // recipe.oidc.key_id, which the server's template.keys must hold
	origin string     // where the key that set kid, or else the one that turned the section on, was set: the origin of the missing-key problem
}

// _emailSchema declares the keys of an item of recipe.github_auth.emails.
var _emailSchema = config.NewSchema(
	config.Field[githubauth.Email]{
		Key: "email", Kind: config.String,
		Set: func(e *githubauth.Email, v config.Value) error { e.Email = v.Str; return nil },
	},
	config.Field[githubauth.Email]{
		Key: "primary", Kind: config.Bool,
		Set: func(e *githubauth.Email, v config.Value) error { e.Primary = v.Bool; return nil },
	},
	config.Field[githubauth.Email]{
		Key: "verified", Kind: config.Bool,
		Set: func(e *githubauth.Email, v config.Value) error { e.Verified = v.Bool; return nil },
	},
	config.Field[githubauth.Email]{
		Key: "visibility", Kind: config.String,
		Set: func(e *githubauth.Email, v config.Value) error { e.Visibility = v.Str; return nil },
	},
)

// _recipeSchema declares the settings of the built-in recipes: the keys of the recipe section of the settings, which
// the HTTP server leaves to the command.
var _recipeSchema = sync.OnceValue(func() *config.Schema[recipes] {
	return config.NewSchema(slices.Concat(
		[]config.Field[recipes]{{Key: "config", Kind: config.String, Hidden: true}},
		inRecipe(
			"github_auth",
			"Mock GitHub's OAuth web flow and the profile of the user who signs in: point the app's github.com and api.github.com base URLs at the server.",
			func(r *recipes) **githubAuthRecipe { return &r.githubAuth },
			func(string) githubAuthRecipe { return githubAuthRecipe{user: githubauth.DefaultUser()} },
			githubAuthFields()...,
		),
		inRecipe(
			"oidc",
			"Mock an OpenID Connect provider: discovery, JWKS, the authorization code flow and userinfo. It signs ID tokens with the server's key recipe.oidc.key_id, which template.keys must set, such as vlatte=RS256.",
			func(r *recipes) **oidcRecipe { return &r.oidc },
			func(origin string) oidcRecipe { return oidcRecipe{kid: oidc.DefaultKeyID, origin: origin} },
			oidcFields()...,
		),
	)...).Only("recipe", "config")
})

// loadRecipes reads the recipe settings from the sources the HTTP server reads, with args: the --recipe* flags and
// --config that splitArgs gives it.
func loadRecipes(args []string) (recipes, error) {
	var rs recipes
	_, err := _recipeSchema().Load(&rs, mockhttp.DefaultConfigSources(args))

	return rs, err
}

// inRecipe returns the keys of the recipe name: the section key, which turns it on or off, and each of fields under
// recipe.<name>, which sets its section and turns it on. def builds the section when a key turns it on, from the
// origin of that key.
func inRecipe[S any](name, usage string, section func(*recipes) **S, def func(origin string) S, fields ...config.Field[S]) []config.Field[recipes] {
	prefix := "recipe." + name

	in := make([]config.Field[recipes], 0, 1+len(fields))
	in = append(in, config.Field[recipes]{
		Key: prefix, Kind: config.Bool, Usage: usage,
		Set: func(r *recipes, v config.Value) error {
			config.Toggle(section(r), def(v.Origin), v.Bool)
			return nil
		},
	})

	for _, f := range fields {
		in = append(in, config.Field[recipes]{
			Key: prefix + "." + f.Key, Short: f.Short, Usage: f.Usage, Default: f.Default, Kind: f.Kind,
			Choices: f.Choices, Hidden: f.Hidden, Secret: f.Secret,
			Set: func(r *recipes, v config.Value) error {
				return f.Set(config.Section(section(r), def(v.Origin)), v)
			},
		})
	}

	return in
}

// githubAuthFields declares the keys of recipe.github_auth, relative to it.
func githubAuthFields() []config.Field[githubAuthRecipe] {
	profile := githubauth.DefaultUser()

	text := func(key, usage, def string, opt func(string) githubauth.Option) config.Field[githubAuthRecipe] {
		return config.Field[githubAuthRecipe]{
			Key: key, Kind: config.String, Usage: usage, Default: def,
			Set: func(r *githubAuthRecipe, v config.Value) error {
				r.opts = append(r.opts, opt(v.Str))
				return nil
			},
		}
	}

	user := func(key, what, def string, set func(u *githubauth.User, s string)) config.Field[githubAuthRecipe] {
		return config.Field[githubAuthRecipe]{
			Key: key, Kind: config.String, Usage: what + " of the user GET /user answers.", Default: def,
			Set: func(r *githubAuthRecipe, v config.Value) error {
				set(&r.user, v.Str)
				return nil
			},
		}
	}

	return []config.Field[githubAuthRecipe]{
		text("client_id", "Client ID of the OAuth app: the authorize and access token endpoints answer only requests that send it.", githubauth.DefaultClientID, githubauth.ClientID),
		text("client_secret", "Client secret of the OAuth app, which the access token endpoint asks for.", githubauth.DefaultClientSecret, githubauth.ClientSecret),
		text("code", "Code the authorize endpoint gives the app, and the only one the access token endpoint exchanges.", githubauth.DefaultCode, githubauth.Code),
		text("token", "Access token the access token endpoint issues, and the only one GET /user and GET /user/emails accept.", githubauth.DefaultToken, githubauth.Token),
		text("scope", "Scope the access token endpoint says the token has.", githubauth.DefaultScope, githubauth.Scope),
		{
			Key: "user.id", Kind: config.Int, Default: strconv.FormatInt(profile.ID, 10),
			Usage: "ID of the user GET /user answers.",
			Set: func(r *githubAuthRecipe, v config.Value) error {
				r.user.ID = v.Int
				return nil
			},
		},
		user("user.login", "Login", profile.Login, func(u *githubauth.User, s string) { u.Login = s }),
		user("user.name", "Name", profile.Name, func(u *githubauth.User, s string) { u.Name = s }),
		user("user.email", "Email", profile.Email, func(u *githubauth.User, s string) { u.Email = s }),
		user("user.avatar_url", "Avatar URL", profile.AvatarURL, func(u *githubauth.User, s string) { u.AvatarURL = s }),
		user("user.html_url", "Profile URL", profile.HTMLURL, func(u *githubauth.User, s string) { u.HTMLURL = s }),
		user("user.type", "Type", profile.Type, func(u *githubauth.User, s string) { u.Type = s }),
		{
			Key: "emails", Kind: config.Any,
			Usage: "Addresses GET /user/emails answers: a list of {email, primary, verified, visibility}, or its JSON text. Without it, the user's email, primary and verified.",
			Set: func(r *githubAuthRecipe, v config.Value) error {
				v, err := parseJSONText(v, "a list")
				if err != nil {
					return err
				}

				// Not nil: Emails takes nil for the profile's email, and an empty list for none.
				list := make([]githubauth.Email, 0)

				err = config.Items(v, func(item config.Value) error {
					var e githubauth.Email
					if err := _emailSchema.Decode(&e, item); err != nil {
						return err
					}

					list = append(list, e)

					return nil
				})
				if err != nil {
					return err
				}

				r.opts = append(r.opts, githubauth.Emails(list...))

				return nil
			},
		},
	}
}

// oidcFields declares the keys of recipe.oidc, relative to it.
func oidcFields() []config.Field[oidcRecipe] {
	paths := oidc.DefaultPaths()

	text := func(key, usage, def string, opt func(string) oidc.Option) config.Field[oidcRecipe] {
		return config.Field[oidcRecipe]{
			Key: key, Kind: config.String, Usage: usage, Default: def,
			Set: func(r *oidcRecipe, v config.Value) error {
				r.opts = append(r.opts, opt(v.Str))
				return nil
			},
		}
	}

	path := func(key, what, def string, set func(p *oidc.Paths, s string)) config.Field[oidcRecipe] {
		return config.Field[oidcRecipe]{
			Key: "paths." + key, Kind: config.String, Usage: "Path of the " + what + " endpoint, under the issuer's path.", Default: def,
			Set: func(r *oidcRecipe, v config.Value) error {
				set(&r.paths, v.Str)
				return nil
			},
		}
	}

	return []config.Field[oidcRecipe]{
		text("client_id", "Client ID of the app: the authorize and token endpoints answer only requests that send it, and ID tokens have it as aud.", oidc.DefaultClientID, oidc.ClientID),
		text("client_secret", "Client secret the token endpoint asks for, in the form.", oidc.DefaultClientSecret, oidc.ClientSecret),
		{
			Key: "key_id", Kind: config.String, Default: oidc.DefaultKeyID,
			Usage: "Kid of the server's key, from template.keys, that ID tokens are signed with.",
			Set: func(r *oidcRecipe, v config.Value) error {
				if v.Str == "" {
					return config.Invalid(v, v.Key+" must not be empty", "set the kid of a key template.keys sets, such as "+oidc.DefaultKeyID)
				}

				r.kid = v.Str
				r.origin = v.Origin

				return nil
			},
		},
		{
			Key: "issuer", Kind: config.String,
			Usage: "Issuer, an absolute URL with no query or fragment: the iss of ID tokens and the base URL of the endpoints discovery announces. Empty is the server's URL. An issuer with a path serves the endpoints under it.",
			Set: func(r *oidcRecipe, v config.Value) error {
				if v.Str != "" && !httpx.IsBaseURL(v.Str) {
					return config.Invalid(v, v.Key+" must be an absolute URL with no query or fragment, such as https://id.example, not "+
						strconv.Quote(v.Str), "")
				}

				r.opts = append(r.opts, oidc.Issuer(v.Str))

				return nil
			},
		},
		text("access_token", "Access token the token endpoint issues, and the only one userinfo accepts.", oidc.DefaultAccessToken, oidc.AccessToken),
		{
			Key: "token_ttl", Kind: config.Duration, Default: oidc.DefaultTokenTTL.String(),
			Usage: "How long ID and access tokens last: milliseconds or a Go duration (1.5s).",
			Set: func(r *oidcRecipe, v config.Value) error {
				r.opts = append(r.opts, oidc.TokenTTL(v.Dur))
				return nil
			},
		},
		{
			Key: "claims", Kind: config.Any,
			Usage: "Claims of the user who signs in, in ID tokens and userinfo: an object, or its JSON text. Without it, the recipe's default user.",
			Set: func(r *oidcRecipe, v config.Value) error {
				v, err := parseJSONText(v, "an object")
				if err != nil {
					return err
				}

				claims, ok := v.Any.(map[string]any)
				if !ok {
					return config.Mismatch(v, "an object")
				}

				r.opts = append(r.opts, oidc.Claims(claims))

				return nil
			},
		},
		path("discovery", "discovery", paths.Discovery, func(p *oidc.Paths, s string) { p.Discovery = s }),
		path("authorize", "authorize", paths.Authorize, func(p *oidc.Paths, s string) { p.Authorize = s }),
		path("token", "token", paths.Token, func(p *oidc.Paths, s string) { p.Token = s }),
		path("jwks", "JWKS", paths.JWKS, func(p *oidc.Paths, s string) { p.JWKS = s }),
		path("userinfo", "userinfo", paths.UserInfo, func(p *oidc.Paths, s string) { p.UserInfo = s }),
	}
}

// parseJSONText returns v with its Any decoded when it is text, as env and flags give it: text is JSON text from any
// source, and a list or an object from a file is returned as is. want says what v takes, for the hint of text that is
// not JSON.
func parseJSONText(v config.Value, want string) (config.Value, error) {
	s, ok := v.Any.(string)
	if !ok {
		return v, nil
	}

	if s == "" {
		return v, config.Invalid(v, v.Key+" is empty", "set "+want+", or its JSON text")
	}

	parsed, err := config.Parse([]byte(s), "json")
	if err != nil {
		return v, config.Invalid(v, v.Key+" is not JSON: "+err.Error(), "set "+want+", or its JSON text")
	}

	v.Any = parsed

	return v, nil
}

// recipe returns the githubauth recipe the section's keys configure.
func (r *githubAuthRecipe) recipe() mockhttp.Recipe {
	return githubauth.New(append(slices.Clone(r.opts), githubauth.WithUser(r.user))...)
}

// recipe returns the oidc recipe the section's keys configure.
func (r *oidcRecipe) recipe() mockhttp.Recipe {
	return oidc.New(append(slices.Clone(r.opts), oidc.KeyID(r.kid), oidc.WithPaths(r.paths))...)
}

// prep runs the recipes the settings turn on, on m. A recipe.oidc without its signing key is a problem with the
// settings: the error is a *config.Error naming template.keys.
func (rs recipes) prep(ctx context.Context, m *mockhttp.Server) error {
	// Each recipe runs on its own, not through m.Prep, whose error names it by its position and Go type: the error
	// names its section instead.
	in := &vlatte.RecipeInput[*mockhttp.Mock, *mockhttp.Server]{Server: m}

	if rs.githubAuth != nil {
		if err := rs.githubAuth.recipe().Prep(ctx, in); err != nil {
			return fmt.Errorf("recipe: preparing recipe.github_auth: %w", err)
		}
	}

	if rs.oidc != nil {
		err := rs.oidc.recipe().Prep(ctx, in)

		switch {
		case errors.Is(err, tmplfunc.ErrUnknownKey):
			return &config.Error{Problems: []config.Problem{{
				Code:    config.CodeMissingValue,
				Origin:  rs.oidc.origin,
				Key:     "template.keys",
				Message: fmt.Sprintf("recipe.oidc signs with key %q, which template.keys does not set", rs.oidc.kid),
				Hint:    fmt.Sprintf("set template.keys, such as %s=RS256", rs.oidc.kid),
			}}}
		case err != nil:
			return fmt.Errorf("recipe: preparing recipe.oidc: %w", err)
		}
	}

	return nil
}
