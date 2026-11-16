package main

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/recipe/http/githubauth"
	"github.com/caffeine-projects/vlatte/recipe/http/oidc"
)

// _noRedirects stops at a redirect, so a test reads where an authorize endpoint sends the browser.
var _noRedirects = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// send sends a method request to target with header, and with form as its body when it is not nil, and returns the
// answer without following a redirect. The answer's body closes when the test ends.
func send(t *testing.T, method, target string, header http.Header, form url.Values) *http.Response {
	t.Helper()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	require.NoError(t, err)
	maps.Copy(req.Header, header)

	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	res, err := _noRedirects.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

func getWithToken(t *testing.T, target, token string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(body)
}

func TestCLI_GitHubAuthRecipeFromFlags(t *testing.T) {
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	addr := randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled",
		"--recipe-github-auth-token", "gho_cli", "--recipe-github-auth-user-login", "alice")

	url := "http://" + addr + githubauth.UserPath
	firstAnswer(t, url)

	status, body := getWithToken(t, url, "gho_cli")
	require.Equal(t, http.StatusOK, status)

	var got githubauth.User
	require.NoError(t, json.Unmarshal([]byte(body), &got))

	want := githubauth.DefaultUser()
	want.Login = "alice"
	require.Equal(t, want, got)

	status, _ = getWithToken(t, url, githubauth.DefaultToken)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestCLI_GitHubAuthRecipeTakesEveryKey(t *testing.T) {
	// Each key reaches its option: a key wired to another option, or to none, changes one of the answers below.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	addr := randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled",
		"--recipe-github-auth-client-id", "app",
		"--recipe-github-auth-client-secret", "s3",
		"--recipe-github-auth-code", "c0de",
		"--recipe-github-auth-token", "tok",
		"--recipe-github-auth-scope", "read:org",
		"--recipe-github-auth-user-id", "7",
		"--recipe-github-auth-user-login", "ana",
		"--recipe-github-auth-user-name", "Ana",
		"--recipe-github-auth-user-email", "ana@example.com",
		"--recipe-github-auth-user-avatar-url", "https://example.com/ana.png",
		"--recipe-github-auth-user-html-url", "https://example.com/ana",
		"--recipe-github-auth-user-type", "Bot",
	)

	base := "http://" + addr
	firstAnswer(t, base+githubauth.UserPath)

	res := send(t, http.MethodGet, base+githubauth.AuthorizePath+"?"+url.Values{
		"client_id":    {"app"},
		"redirect_uri": {"https://app.example/cb"},
	}.Encode(), nil, nil)
	require.Equal(t, http.StatusFound, res.StatusCode)
	require.Equal(t, "https://app.example/cb?code=c0de", res.Header.Get(mockhttp.HeaderLocation))

	res = send(t, http.MethodPost, base+githubauth.AccessTokenPath, http.Header{"Accept": {"application/json"}}, url.Values{
		"client_id":     {"app"},
		"client_secret": {"s3"},
		"code":          {"c0de"},
	})
	require.Equal(t, http.StatusOK, res.StatusCode)

	var token map[string]string
	require.NoError(t, json.UnmarshalRead(res.Body, &token))
	require.Equal(t, map[string]string{"access_token": "tok", "token_type": "bearer", "scope": "read:org"}, token)

	status, body := getWithToken(t, base+githubauth.UserPath, "tok")
	require.Equal(t, http.StatusOK, status)

	var user githubauth.User
	require.NoError(t, json.Unmarshal([]byte(body), &user))
	require.Equal(t, githubauth.User{
		ID:        7,
		Login:     "ana",
		Name:      "Ana",
		Email:     "ana@example.com",
		AvatarURL: "https://example.com/ana.png",
		HTMLURL:   "https://example.com/ana",
		Type:      "Bot",
	}, user)
}

func TestCLI_OIDCRecipeTakesEveryKey(t *testing.T) {
	// Each key reaches its option: a key wired to another option, or to none, changes one of the answers below.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	addr := randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled", "--template-keys", "k2=RS256",
		"--recipe-oidc-client-id", "app",
		"--recipe-oidc-client-secret", "s3",
		"--recipe-oidc-key-id", "k2",
		"--recipe-oidc-issuer", "https://id.example",
		"--recipe-oidc-access-token", "at",
		"--recipe-oidc-token-ttl", "5m",
		"--recipe-oidc-paths-discovery", "/d",
		"--recipe-oidc-paths-authorize", "/a",
		"--recipe-oidc-paths-token", "/t",
		"--recipe-oidc-paths-jwks", "/j",
		"--recipe-oidc-paths-userinfo", "/u",
	)

	base := "http://" + addr

	status, body := firstAnswer(t, base+"/d")
	require.Equal(t, http.StatusOK, status)

	var discovery map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &discovery))
	require.Equal(t, "https://id.example", discovery["issuer"])
	require.Equal(t, "https://id.example/a", discovery["authorization_endpoint"])
	require.Equal(t, "https://id.example/t", discovery["token_endpoint"])
	require.Equal(t, "https://id.example/j", discovery["jwks_uri"])
	require.Equal(t, "https://id.example/u", discovery["userinfo_endpoint"])

	status, body = firstAnswer(t, base+"/j")
	require.Equal(t, http.StatusOK, status)

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &jwks))
	require.Len(t, jwks.Keys, 1)
	require.Equal(t, "k2", jwks.Keys[0].Kid)

	res := send(t, http.MethodGet, base+"/a?"+url.Values{
		"response_type": {"code"},
		"client_id":     {"app"},
		"redirect_uri":  {"https://app.example/cb"},
		"scope":         {"openid"},
	}.Encode(), nil, nil)
	require.Equal(t, http.StatusFound, res.StatusCode)

	location, err := url.Parse(res.Header.Get(mockhttp.HeaderLocation))
	require.NoError(t, err)

	res = send(t, http.MethodPost, base+"/t", nil, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {"app"},
		"client_secret": {"s3"},
		"redirect_uri":  {"https://app.example/cb"},
		"code":          {location.Query().Get("code")},
	})
	require.Equal(t, http.StatusOK, res.StatusCode)

	var tokens map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &tokens))
	require.Equal(t, "at", tokens["access_token"])
	require.InDelta(t, 300, tokens["expires_in"], 0)

	status, _ = getWithToken(t, base+"/u", "at")
	require.Equal(t, http.StatusOK, status)
}

func TestCLI_GitHubAuthEmailsFromTheFileOrJSONText(t *testing.T) {
	want := []githubauth.Email{{Email: "a@x.io", Primary: true, Verified: true, Visibility: "public"}}

	for name, setup := range map[string]func(t *testing.T){
		"file": func(t *testing.T) {
			content := "recipe: {github_auth: {emails: [{email: a@x.io, primary: true, verified: true, visibility: public}]}}\n"
			require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte(content), 0o600))
		},
		"env": func(t *testing.T) {
			t.Setenv("VLATTE_RECIPE_GITHUB_AUTH_EMAILS", `[{"email":"a@x.io","primary":true,"verified":true,"visibility":"public"}]`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			withoutDockerHost(t)
			t.Chdir(t.TempDir())
			setup(t)

			addr := randomAddr(t)
			start(t, "--addr", addr, "--log-level", "disabled", "--recipe-github-auth")

			url := "http://" + addr + githubauth.UserEmailsPath
			firstAnswer(t, url)

			status, body := getWithToken(t, url, githubauth.DefaultToken)
			require.Equal(t, http.StatusOK, status)

			var got []githubauth.Email
			require.NoError(t, json.Unmarshal([]byte(body), &got))
			require.Equal(t, want, got)
		})
	}
}

func TestCLI_OIDCRecipeFromTheFileAndFlags(t *testing.T) {
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	content := "template: {keys: [vlatte=RS256]}\nrecipe: {oidc: {client_id: app, paths: {token: /oauth/token}}}\n"
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte(content), 0o600))

	addr := randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled", "--recipe-oidc-claims", `{"sub":"alice"}`)

	status, body := firstAnswer(t, "http://"+addr+"/.well-known/openid-configuration")
	require.Equal(t, http.StatusOK, status)

	var discovery map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &discovery))
	require.Equal(t, "http://"+addr+"/oauth/token", discovery["token_endpoint"])

	status, body = getWithToken(t, "http://"+addr+"/userinfo", oidc.DefaultAccessToken)
	require.Equal(t, http.StatusOK, status)

	var claims map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &claims))
	require.Equal(t, map[string]any{"sub": "alice"}, claims)
}

func TestCLI_ARecipeTurnedOffAddsNoMocks(t *testing.T) {
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	content := "template: {keys: [vlatte=RS256]}\nrecipe: {oidc: {client_id: app}}\n"
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte(content), 0o600))

	addr := randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled", "--recipe-oidc=false")

	status, _ := firstAnswer(t, "http://"+addr+"/.well-known/openid-configuration")
	require.Equal(t, mockhttp.StatusNoMatch, status)
}

func TestRun_OIDCRecipeWithoutAKeyNamesTheSetting(t *testing.T) {
	withoutDockerHost(t)
	t.Chdir(t.TempDir())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stderr bytes.Buffer
	code := run(ctx, []string{"--addr", randomAddr(t), "--recipe-oidc"}, nil, io.Discard, &stderr)

	require.Equal(t, 2, code, stderr.String())
	require.Contains(t, stderr.String(),
		`flag --recipe-oidc: missing_value: recipe.oidc signs with key "vlatte", which template.keys does not set; set template.keys, such as vlatte=RS256`)
}

func TestRun_OIDCRecipeWithoutItsKeyNamesTheKeyIDSetting(t *testing.T) {
	// The kid that template.keys lacks came from recipe.oidc.key_id: the problem points there, not at another key of
	// the section.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte("recipe: {oidc: {client_id: app}}\n"), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stderr bytes.Buffer
	code := run(ctx, []string{"--addr", randomAddr(t), "--recipe-oidc-key-id", "other"}, nil, io.Discard, &stderr)

	require.Equal(t, 2, code, stderr.String())
	require.Contains(t, stderr.String(),
		`flag --recipe-oidc-key-id: missing_value: recipe.oidc signs with key "other", which template.keys does not set; set template.keys, such as other=RS256`)
}

func TestRun_OIDCRecipeTurnedOnAgainNamesWhereItWas(t *testing.T) {
	// A source that turns the section off drops the key_id of the sources before it: the problem points at the key
	// that turned it on again, not at a key_id the recipe no longer uses.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte("recipe: {oidc: {key_id: other}}\n"), 0o600))
	t.Setenv("VLATTE_RECIPE_OIDC", "false")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stderr bytes.Buffer
	code := run(ctx, []string{"--addr", randomAddr(t), "--recipe-oidc"}, nil, io.Discard, &stderr)

	require.Equal(t, 2, code, stderr.String())
	require.Contains(t, stderr.String(),
		`flag --recipe-oidc: missing_value: recipe.oidc signs with key "vlatte", which template.keys does not set`)
}

func TestRun_OIDCRecipeTurnedOnAgainByAKeyNamesThatKey(t *testing.T) {
	// A key that turns the section on again, after a source turned it off, is where it was set: not the source that
	// turned it off.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte("recipe: {oidc: {client_id: app}}\n"), 0o600))
	t.Setenv("VLATTE_RECIPE_OIDC", "false")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var stderr bytes.Buffer
	code := run(ctx, []string{"--addr", randomAddr(t), "--recipe-oidc-issuer", "http://x"}, nil, io.Discard, &stderr)

	require.Equal(t, 2, code, stderr.String())
	require.Contains(t, stderr.String(),
		`flag --recipe-oidc-issuer: missing_value: recipe.oidc signs with key "vlatte", which template.keys does not set`)
}

func TestCLI_AnEmptyEmailsListAnswersNone(t *testing.T) {
	// An empty list is no addresses, not the profile's email, which the recipe answers without emails.
	withoutDockerHost(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte("recipe: {github_auth: {emails: []}}\n"), 0o600))

	addr := randomAddr(t)
	start(t, "--addr", addr, "--log-level", "disabled")

	url := "http://" + addr + githubauth.UserEmailsPath
	firstAnswer(t, url)

	status, body := getWithToken(t, url, githubauth.DefaultToken)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `[]`, body)
}

func TestRun_RecipeSettingErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		env  map[string]string
		file string
		want string
	}{
		{
			name: "claims not JSON",
			args: []string{"--recipe-oidc-claims={bad"},
			want: "flag --recipe-oidc-claims: invalid_value: recipe.oidc.claims is not JSON: ",
		},
		{
			name: "claims empty",
			args: []string{"--recipe-oidc-claims="},
			want: "flag --recipe-oidc-claims: invalid_value: recipe.oidc.claims is empty; set an object, or its JSON text",
		},
		{
			name: "claims a list",
			args: []string{"--recipe-oidc-claims=[1]"},
			want: "flag --recipe-oidc-claims: invalid_value: recipe.oidc.claims must be an object, not a list",
		},
		{
			name: "emails an object",
			args: []string{`--recipe-github-auth-emails={"email":"a"}`},
			want: "flag --recipe-github-auth-emails: invalid_value: recipe.github_auth.emails must be a list, not an object",
		},
		{
			name: "user.id not an int",
			args: []string{"--recipe-github-auth-user-id=x"},
			want: `flag --recipe-github-auth-user-id: invalid_value: recipe.github_auth.user.id must be an integer, not "x"`,
		},
		{
			name: "bad token_ttl",
			args: []string{"--recipe-oidc-token-ttl=nope"},
			want: `flag --recipe-oidc-token-ttl: invalid_value: recipe.oidc.token_ttl must be milliseconds or a Go duration, not "nope"; such as 1500 or 1.5s`,
		},
		{
			name: "empty key_id",
			args: []string{"--recipe-oidc-key-id="},
			want: "flag --recipe-oidc-key-id: invalid_value: recipe.oidc.key_id must not be empty; set the kid of a key template.keys sets, such as vlatte",
		},
		{
			name: "mistyped flag",
			args: []string{"--recipe-oidc-clientid=app"},
			want: "unknown_key: --recipe-oidc-clientid is not a known flag; did you mean --recipe-oidc-client-id?",
		},
		{
			name: "mistyped env",
			env:  map[string]string{"VLATTE_RECIPE_OIDC_CLIENTID": "app"},
			want: "unknown_key: VLATTE_RECIPE_OIDC_CLIENTID is not a known setting; did you mean VLATTE_RECIPE_OIDC_CLIENT_ID?",
		},
		{
			name: "mistyped file key",
			file: "recipe: {oidc: {clientid: app}}\n",
			want: "file .vlatterc.yaml: unknown_key: recipe.oidc.clientid is not a known setting; did you mean recipe.oidc.client_id?",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withoutDockerHost(t)
			t.Chdir(t.TempDir())

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			if tt.file != "" {
				require.NoError(t, os.WriteFile(".vlatterc.yaml", []byte(tt.file), 0o600))
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var stderr bytes.Buffer
			code := run(ctx, append([]string{"--addr", randomAddr(t)}, tt.args...), nil, io.Discard, &stderr)

			require.Equal(t, 2, code, stderr.String())
			require.Contains(t, stderr.String(), tt.want)
		})
	}
}
