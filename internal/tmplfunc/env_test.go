package tmplfunc

import (
	"fmt"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

// envFuncs returns Funcs with the env of e.
func envFuncs(e *Env) map[string]any {
	funcs := Funcs()
	funcs["env"] = e.Get

	return funcs
}

func TestEnv(t *testing.T) {
	t.Setenv("VLATTE_TEST_TENANT", "acme")
	t.Setenv("VLATTE_TEST_SECRET", "s3cr3t")
	t.Setenv("VLATTE_TEST_EMPTY", "")

	var e Env
	require.NoError(t, e.Allow("VLATTE_TEST_TENANT", "VLATTE_TEST_EMPTY", "VLATTE_TEST_UNSET"))

	funcs := envFuncs(&e)

	t.Run("reads an allowed variable", func(t *testing.T) {
		out, err := render(t, funcs, `{{ env "VLATTE_TEST_TENANT" }} {{ "VLATTE_TEST_TENANT" | env }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "acme acme", out)
	})

	t.Run("an allowed variable empty or unset is empty, for default", func(t *testing.T) {
		out, err := render(t, funcs, `{{ env "VLATTE_TEST_EMPTY" | default "a" }} {{ env "VLATTE_TEST_UNSET" | default "b" }}`, nil)
		require.NoError(t, err)
		require.Equal(t, "a b", out)
	})

	t.Run("a variable not allowed fails, without its value", func(t *testing.T) {
		out, err := render(t, funcs, `{{ env .name }}`, map[string]any{"name": "VLATTE_TEST_SECRET"})
		require.ErrorIs(t, err, ErrEnvNotAllowed)
		require.ErrorContains(t, err, `"VLATTE_TEST_SECRET" is not allowed`)
		require.NotContains(t, err.Error(), "s3cr3t")
		require.Empty(t, out)
	})

	t.Run("names are exact", func(t *testing.T) {
		_, err := e.Get("vlatte_test_tenant")
		require.ErrorIs(t, err, ErrEnvNotAllowed)
	})

	t.Run("the zero Env allows none", func(t *testing.T) {
		var none Env

		_, err := none.Get("VLATTE_TEST_TENANT")
		require.ErrorIs(t, err, ErrEnvNotAllowed)
	})

	t.Run("an invalid name is an error", func(t *testing.T) {
		for _, name := range []string{"", "A=B", "A\x00"} {
			var invalid Env
			require.EqualError(t, invalid.Allow(name), fmt.Sprintf("environment variable name %q is invalid", name))
		}
	})
}

func TestEnv_Check(t *testing.T) {
	var e Env
	require.NoError(t, e.Allow("VLATTE_TEST_TENANT"))

	parse := func(t *testing.T, text string) *template.Template {
		t.Helper()

		tmpl, err := template.New("body").Funcs(envFuncs(&e)).Parse(text)
		require.NoError(t, err)

		return tmpl
	}

	t.Run("finds a name given as a literal that isn't allowed, wherever it is", func(t *testing.T) {
		texts := []string{
			`{{ env "VLATTE_TEST_SECRET" }}`,
			`{{ "VLATTE_TEST_SECRET" | env }}`,
			`{{ if true }}{{ with 1 }}{{ printf "%s" (env "VLATTE_TEST_SECRET") }}{{ end }}{{ end }}`,
			`{{ range list 1 }}{{ else }}{{ env "VLATTE_TEST_SECRET" }}{{ end }}`,
			`{{ define "x" }}{{ env "VLATTE_TEST_SECRET" }}{{ end }}`,
			`{{ define "x" }}{{ . }}{{ end }}{{ template "x" (env "VLATTE_TEST_SECRET") }}`,
		}

		for _, text := range texts {
			err := e.Check(parse(t, text))
			require.EqualError(t, err, `"VLATTE_TEST_SECRET" is not allowed`, text)
			require.ErrorIs(t, err, ErrEnvNotAllowed)
		}
	})

	t.Run("passes allowed names, names from data and templates without data", func(t *testing.T) {
		texts := []string{
			`{{ env "VLATTE_TEST_TENANT" }}`,
			`{{ env .name }}`,
			`{{ define "x" }}{{ env "VLATTE_TEST_TENANT" }}{{ end }}{{ template "x" }}`,
			`text only`,
		}

		for _, text := range texts {
			require.NoError(t, e.Check(parse(t, text)), text)
		}
	})
}
