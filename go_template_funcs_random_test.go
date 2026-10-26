package vlatte

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRandomString(t *testing.T) {
	e := MustNewGoTemplateEngine()

	for kind, alphabet := range _alphabets {
		t.Run(kind+" has the length asked, from its alphabet", func(t *testing.T) {
			out, err := render(t, e, `{{ randomString .kind 64 }}`, map[string]any{"kind": kind})
			require.NoError(t, err)
			require.Len(t, out, 64)

			for _, r := range out {
				require.Contains(t, alphabet, string(r))
			}
		})
	}

	runFuncCases(t, []funcCase{
		{name: "an empty string", text: `[{{ randomString "hex" 0 }}]`, want: "[]"},
		{name: "an unknown kind", text: `{{ randomString "emoji" 3 }}`, err: `error calling randomString: unknown kind "emoji"`},
		{name: "a negative length", text: `{{ randomString "hex" -1 }}`, err: "error calling randomString: negative length -1"},
	})
}

func TestRandomNumbers(t *testing.T) {
	e := MustNewGoTemplateEngine()

	t.Run("randomInt is in [min, max)", func(t *testing.T) {
		out, err := render(t, e, `{{ range 200 }}{{ randomInt -2 3 }} {{ end }}`, nil)
		require.NoError(t, err)

		seen := make(map[int]bool)
		for f := range strings.FieldsSeq(out) {
			n, err := strconv.Atoi(f)
			require.NoError(t, err)
			require.True(t, n >= -2 && n < 3, "%d is out of [-2, 3)", n)
			seen[n] = true
		}

		// 200 draws of 5 values miss one with a chance of about 1e-19.
		require.Len(t, seen, 5)
	})

	t.Run("randomFloat is in [min, max)", func(t *testing.T) {
		out, err := render(t, e, `{{ range 100 }}{{ randomFloat 1.5 2 }} {{ end }}`, nil)
		require.NoError(t, err)

		for f := range strings.FieldsSeq(out) {
			n, err := strconv.ParseFloat(f, 64)
			require.NoError(t, err)
			require.True(t, n >= 1.5 && n < 2, "%v is out of [1.5, 2)", n)
		}
	})

	runFuncCases(t, []funcCase{
		{name: "randomInt over the whole range of int", text: `{{ randomInt -9223372036854775807 9223372036854775807 | printf "%T" }}`, want: "int"},
		{name: "randomFloat with a span too large", text: `{{ randomFloat -1.7e308 1.7e308 }}`, err: "error calling randomFloat: max - min overflows float64"},
		{name: "randomInt with max not above min", text: `{{ randomInt 3 3 }}`, err: "error calling randomInt: max must be above min"},
		{name: "randomFloat with max not above min", text: `{{ randomFloat 2 1 }}`, err: "error calling randomFloat: max must be above min"},
	})
}
