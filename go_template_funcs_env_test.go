package vlatte

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnv(t *testing.T) {
	t.Setenv("VLATTE_TEST_TENANT", "acme")
	t.Setenv("VLATTE_TEST_SECRET", "s3cr3t")
	t.Setenv("VLATTE_TEST_EMPTY", "")

	e := MustNewGoTemplateEngine(WithEnv("VLATTE_TEST_TENANT", "VLATTE_TEST_EMPTY", "VLATTE_TEST_UNSET"))

	t.Run("reads an allowed variable", func(t *testing.T) {
		out, err := render(t, e, `{{ env "VLATTE_TEST_TENANT" }} {{ "VLATTE_TEST_TENANT" | env }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "acme acme", out)
	})

	t.Run("an allowed variable empty or unset is empty, for default", func(t *testing.T) {
		out, err := render(t, e, `{{ env "VLATTE_TEST_EMPTY" | default "a" }} {{ env "VLATTE_TEST_UNSET" | default "b" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "a b", out)
	})

	t.Run("a variable not allowed fails the template when it compiles", func(t *testing.T) {
		texts := []string{
			`{{ env "VLATTE_TEST_SECRET" }}`,
			`{{ "VLATTE_TEST_SECRET" | env }}`,
			`{{ if true }}{{ with 1 }}{{ printf "%s" (env "VLATTE_TEST_SECRET") }}{{ end }}{{ end }}`,
			`{{ define "x" }}{{ env "VLATTE_TEST_SECRET" }}{{ end }}`,
			`{{ define "x" }}{{ . }}{{ end }}{{ template "x" (env "VLATTE_TEST_SECRET") }}`,
		}

		for _, text := range texts {
			_, err := e.Compile("body", text)
			require.EqualError(t, err, `template: body: env: "VLATTE_TEST_SECRET" is not allowed`, text)
			require.ErrorIs(t, err, errEnvNotAllowed)
		}
	})

	t.Run("a template that calls another without data compiles", func(t *testing.T) {
		out, err := render(t, e, `{{ define "x" }}{{ env "VLATTE_TEST_TENANT" }}{{ end }}{{ template "x" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "acme", out)
	})

	t.Run("a name read from data is checked when it runs", func(t *testing.T) {
		tmpl, err := e.Compile("body", `{{ env .name }}`)
		require.NoError(t, err)

		var b strings.Builder
		err = tmpl.Execute(&b, map[string]any{"name": "VLATTE_TEST_SECRET"})
		require.ErrorIs(t, err, errEnvNotAllowed)
		require.NotContains(t, err.Error(), "s3cr3t")
		require.Empty(t, b.String())
	})

	t.Run("names are exact", func(t *testing.T) {
		_, err := e.Compile("body", `{{ env "vlatte_test_tenant" }}`)
		require.ErrorIs(t, err, errEnvNotAllowed)
	})

	t.Run("an engine without WithEnv reads none", func(t *testing.T) {
		_, err := MustNewGoTemplateEngine().Compile("body", `{{ env "VLATTE_TEST_TENANT" }}`)
		require.ErrorIs(t, err, errEnvNotAllowed)
	})

	t.Run("an env of the user's own is not checked", func(t *testing.T) {
		own := MustNewGoTemplateEngine(WithTemplateFuncs(map[string]any{"env": func(string) string { return "own" }}))

		out, err := render(t, own, `{{ env "VLATTE_TEST_SECRET" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "own", out)
	})

	t.Run("an invalid name is an error", func(t *testing.T) {
		for _, name := range []string{"", "A=B", "A\x00"} {
			_, err := NewGoTemplateEngine(WithEnv(name))
			require.ErrorContains(t, err, fmt.Sprintf("vlatte: go template engine: environment variable name %q is invalid", name))
		}
	})
}
