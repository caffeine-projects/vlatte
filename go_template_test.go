package vlatte

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
