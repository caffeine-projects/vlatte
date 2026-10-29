package vlatte

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/jose"
	"github.com/caffeine-projects/vlatte/internal/tmplfunc"
)

// render compiles text with e, then executes it with data.
func render(t *testing.T, e TemplateEngine, text string, data any) (string, error) {
	t.Helper()

	tmpl, err := e.Compile("t", text)
	require.NoError(t, err)

	var b strings.Builder
	err = tmpl.Execute(&b, data)

	return b.String(), err
}

func TestGoTemplateEngine_MissingKeys(t *testing.T) {
	data := map[string]any{"name": "vlatte"}

	t.Run("a missing key is an error, so a typo doesn't render", func(t *testing.T) {
		_, err := render(t, MustNewGoTemplateEngine(), "{{ .nmae }}", data)
		require.ErrorContains(t, err, `map has no entry for key "nmae"`)
	})

	t.Run("index reads an optional key", func(t *testing.T) {
		out, err := render(t, MustNewGoTemplateEngine(), `{{ with index . "nick" }}{{ . }}{{ else }}none{{ end }}`, data)
		require.NoError(t, err)
		require.Equal(t, "none", out)
	})

	t.Run("default prints no value", func(t *testing.T) {
		out, err := render(t, MustNewGoTemplateEngine(WithMissingKey("default")), "{{ .nick }}", data)
		require.NoError(t, err)
		require.Equal(t, "<no value>", out)
	})

	t.Run("zero prints the zero value of the map's elements", func(t *testing.T) {
		out, err := render(t, MustNewGoTemplateEngine(WithMissingKey("zero")), "{{ .count }}", make(map[string]int))
		require.NoError(t, err)
		require.Equal(t, "0", out)
	})

	t.Run("an unknown mode is an error", func(t *testing.T) {
		_, err := NewGoTemplateEngine(WithMissingKey("strict"))
		require.ErrorContains(t, err, `missing key mode "strict"`)

		// MustNewGoTemplateEngine panics with that error.
		require.PanicsWithError(t, err.Error(), func() { MustNewGoTemplateEngine(WithMissingKey("strict")) })
	})
}

func TestGoTemplateEngine_Funcs(t *testing.T) {
	t.Run("templates call the functions of the engine", func(t *testing.T) {
		e := MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"shout": strings.ToUpper}))

		out, err := render(t, e, `{{ shout "hi" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "HI", out)
	})

	t.Run("a function replaces the built-in one of the same name", func(t *testing.T) {
		e := MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"uuid": func() string { return "fixed" }}))

		out, err := render(t, e, "{{ uuid }}", nil)
		require.NoError(t, err)
		require.Equal(t, "fixed", out)
	})

	t.Run("engines don't share functions", func(t *testing.T) {
		MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"shout": strings.ToUpper}))

		_, err := MustNewGoTemplateEngine().Compile("t", `{{ shout "hi" }}`)
		require.ErrorContains(t, err, `function "shout" not defined`)
	})

	// text/template panics on these: the engine must return an error instead.
	bad := []struct {
		name  string
		funcs map[string]any
	}{
		{name: "a name a template can't call", funcs: map[string]any{"not-valid": strings.ToUpper}},
		{name: "a value that isn't a function", funcs: map[string]any{"answer": 42}},
		{name: "a nil function", funcs: map[string]any{"nothing": nil}},
		{name: "a function with three results", funcs: map[string]any{"three": func() (int, int, int) { return 1, 2, 3 }}},
	}

	for _, tc := range bad {
		t.Run(tc.name+" is an error", func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { _, err = NewGoTemplateEngine(WithTemplateFuncs(tc.funcs)) })
			require.ErrorContains(t, err, "vlatte: go template engine: template functions: ")
		})
	}
}

func TestGoTemplateEngine_Compile(t *testing.T) {
	t.Run("errors are text/template's, naming the template", func(t *testing.T) {
		_, err := MustNewGoTemplateEngine().Compile("body", "{{ .X")
		require.EqualError(t, err, "template: body:1: unclosed action")
	})

	t.Run("an engine not made by NewGoTemplateEngine fails instead of panicking", func(t *testing.T) {
		var err error
		require.NotPanics(t, func() { _, err = (&GoTemplateEngine{}).Compile("body", "x") })
		require.ErrorContains(t, err, "NewGoTemplateEngine")
	})

	t.Run("compiling and executing are safe for concurrent use", func(t *testing.T) {
		e := MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"shout": strings.ToUpper}))

		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				tmpl, err := e.Compile("t", `{{ shout .name }}-{{ .n }}`)
				if !assert.NoError(t, err) {
					return
				}

				var b strings.Builder
				assert.NoError(t, tmpl.Execute(&b, map[string]any{"name": "go", "n": i}))
				assert.Equal(t, "GO-"+strconv.Itoa(i), b.String())
			})
		}

		wg.Wait()
	})
}

func TestGoTemplateEngine_Env(t *testing.T) {
	t.Setenv("VLATTE_TEST_TENANT", "acme")

	t.Run("reads the variables WithEnv allows", func(t *testing.T) {
		out, err := render(t, MustNewGoTemplateEngine(WithEnv("VLATTE_TEST_TENANT")), `{{ env "VLATTE_TEST_TENANT" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "acme", out)
	})

	t.Run("a literal name not allowed fails the template when it compiles", func(t *testing.T) {
		_, err := MustNewGoTemplateEngine().Compile("body", `{{ env "VLATTE_TEST_TENANT" }}`)
		require.EqualError(t, err, `template: body: env: "VLATTE_TEST_TENANT" is not allowed`)
		require.ErrorIs(t, err, tmplfunc.ErrEnvNotAllowed)
	})

	t.Run("an env of the user's own is not checked", func(t *testing.T) {
		own := MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"env": func(string) string { return "own" }}))

		out, err := render(t, own, `{{ env "VLATTE_TEST_TENANT" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "own", out)
	})

	t.Run("an invalid name is an error", func(t *testing.T) {
		_, err := NewGoTemplateEngine(WithEnv("A=B"))
		require.EqualError(t, err, fmt.Sprintf("vlatte: go template engine: environment variable name %q is invalid", "A=B"))
	})
}

func TestGoTemplateEngine_SigningKeys(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	e := MustNewGoTemplateEngine(WithSigningKey("key-1", "ES256", key))

	t.Run("signs a token the engine's JWKS verifies", func(t *testing.T) {
		token, err := render(t, e, `{{ jwt "key-1" .claims }}`, map[string]any{"claims": `{"sub":"ana"}`})
		require.NoError(t, err)

		jwks, err := render(t, e, `{{ jwks "key-1" }}`, nil)
		require.NoError(t, err)

		ks, err := jose.ParseKeySet([]byte(jwks))
		require.NoError(t, err)

		tok, err := jose.Parse(token)
		require.NoError(t, err)
		require.NoError(t, ks.Verify(tok))
		require.Equal(t, map[string]any{"sub": "ana"}, tok.Claims)
	})

	t.Run("a literal key the engine lacks fails the template when it compiles", func(t *testing.T) {
		_, err := e.Compile("body", `{{ jwks "key-1" "key-2" }}`)
		require.EqualError(t, err, `template: body: "key-2" is not a signing key of the server`)
		require.ErrorIs(t, err, tmplfunc.ErrUnknownKey)
	})

	t.Run("an engine without keys signs nothing", func(t *testing.T) {
		_, err := MustNewGoTemplateEngine().Compile("body", `{{ jwt "key-1" "{}" }}`)
		require.ErrorIs(t, err, tmplfunc.ErrUnknownKey)
	})

	t.Run("a jwt of the user's own is not checked", func(t *testing.T) {
		own := MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"jwt": func(string, any) string { return "own" }}))

		out, err := render(t, own, `{{ jwt "key-9" "{}" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "own", out)
	})

	t.Run("an HMAC key is refused", func(t *testing.T) {
		_, err := NewGoTemplateEngine(WithSigningKey("key-1", "HS256", key))
		require.ErrorIs(t, err, jose.ErrUnsupportedAlg)
	})

	t.Run("a key added twice is refused", func(t *testing.T) {
		_, err := NewGoTemplateEngine(WithSigningKey("key-1", "ES256", key), WithSigningKey("key-1", "ES256", key))
		require.EqualError(t, err, `vlatte: go template engine: signing key "key-1" is added twice`)
	})
}

// provider is a TemplateDataProvider of a string input that gives what its function returns.
type provider struct {
	key  string
	data func(in string) (any, error)
}

func (p provider) Key() string { return p.key }

func (p provider) Data(in string) (any, error) { return p.data(in) }

func TestProvideTemplateData_GivesEachProvidersDataByItsKey(t *testing.T) {
	providers := []TemplateDataProvider[string]{
		provider{key: "echo", data: func(in string) (any, error) { return in, nil }},
		provider{key: "n", data: func(string) (any, error) { return 1, nil }},
	}

	data, err := ProvideTemplateData(providers, "call")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"echo": "call", "n": 1}, data)
}

func TestProvideTemplateData_AProviderThatFailsNamesItself(t *testing.T) {
	// The server answers with the error: it says which provider failed, as a server can have several.
	errDown := errors.New("user service down")
	providers := []TemplateDataProvider[string]{
		provider{key: "user", data: func(string) (any, error) { return nil, errDown }},
	}

	_, err := ProvideTemplateData(providers, "call")
	require.ErrorIs(t, err, errDown)
	require.EqualError(t, err, "template data provider user: user service down")
}

func TestCheckTemplateDataProviders_RefusesTwoOfOneKey(t *testing.T) {
	// Templates could read only one of them: a setup mistake, which the server fails to start with.
	a := provider{key: "a", data: func(string) (any, error) { return nil, nil }}
	b := provider{key: "b", data: func(string) (any, error) { return nil, nil }}

	require.NoError(t, CheckTemplateDataProviders([]TemplateDataProvider[string]{a, b}))

	err := CheckTemplateDataProviders([]TemplateDataProvider[string]{a, b, a})
	require.ErrorIs(t, err, ErrTemplateDataKeyExists)
	require.ErrorContains(t, err, `"a"`)
}
