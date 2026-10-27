package tmplfunc

import (
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"
	"uuid"

	"github.com/stretchr/testify/require"
)

// render runs text with funcs on data as the engine does, a missing key being an error.
func render(t *testing.T, funcs map[string]any, text string, data any) (string, error) {
	t.Helper()

	tmpl, err := template.New("t").Option("missingkey=error").Funcs(funcs).Parse(text)
	require.NoError(t, err)

	var b strings.Builder
	err = tmpl.Execute(&b, data)

	return b.String(), err
}

// funcCase is a template that renders to want, or fails with an error that contains err.
type funcCase struct {
	name string
	text string
	data any
	want string
	err  string
}

// runFuncCases renders each case with Funcs.
func runFuncCases(t *testing.T, cases []funcCase) {
	t.Helper()

	e := Funcs()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := render(t, e, tc.text, tc.data)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
}

func TestQueryFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "queryUnescape undoes urlquery", text: `{{ urlquery . | queryUnescape }}`, data: "a b&c=d/é", want: "a b&c=d/é"},
		{name: "queryUnescape of a bad escape", text: `{{ queryUnescape "%zz" }}`, err: `error calling queryUnescape: invalid URL escape "%zz"`},
		{
			name: "parseQuery reads form text, each value decoded", text: `{{ $q := parseQuery . }}{{ $q.Get "a" }} {{ index $q.b 1 }}`,
			data: "a=1+2&b=x&b=y%26z", want: "1 2 y&z",
		},
		{name: "parseQuery of a bad escape", text: `{{ parseQuery "a=%zz" }}`, err: `error calling parseQuery: invalid URL escape "%zz"`},
	})
}

func TestFuncs(t *testing.T) {
	e := Funcs()

	t.Run("toJSON writes values a JSON body can hold", func(t *testing.T) {
		data := map[string]any{"name": `a "b" <c>`, "tags": map[string]any{"z": 1, "a": 2}}

		out, err := render(t, e, `{"name": {{ toJSON .name }}, "tags": {{ toJSON .tags }}}`, data)
		require.NoError(t, err)
		// Quotes escaped, no HTML escaping, and keys sorted so responses are stable.
		require.Equal(t, `{"name": "a \"b\" <c>", "tags": {"a":2,"z":1}}`, out)
	})

	t.Run("fromJSON reads the fields of JSON text", func(t *testing.T) {
		out, err := render(t, e, `{{ index (fromJSON .) "id" }}`, `{"id": "42"}`)
		require.NoError(t, err)
		require.Equal(t, "42", out)
	})

	t.Run("fromJSON fails on text that isn't JSON", func(t *testing.T) {
		_, err := render(t, e, `{{ fromJSON . }}`, `{"id": `)
		require.Error(t, err)
	})

	t.Run("uuid is a new UUID on each call", func(t *testing.T) {
		out, err := render(t, e, "{{ uuid }} {{ uuid }}", nil)
		require.NoError(t, err)

		first, second, _ := strings.Cut(out, " ")
		_, err = uuid.Parse(first)
		require.NoError(t, err)
		_, err = uuid.Parse(second)
		require.NoError(t, err)
		require.NotEqual(t, first, second)
	})

	t.Run("now is the current time", func(t *testing.T) {
		before := time.Now().UnixNano()

		out, err := render(t, e, "{{ (now).UnixNano }}", nil)
		require.NoError(t, err)

		nanos, err := strconv.ParseInt(out, 10, 64)
		require.NoError(t, err)
		require.GreaterOrEqual(t, nanos, before)
		require.LessOrEqual(t, nanos, time.Now().UnixNano())
	})

	t.Run("now prints without the monotonic clock", func(t *testing.T) {
		out, err := render(t, e, "{{ now }}", nil)
		require.NoError(t, err)
		require.NotContains(t, out, "m=")
	})

	t.Run("base64Encode and base64Decode are inverses", func(t *testing.T) {
		out, err := render(t, e, `{{ base64Encode . }} {{ base64Decode (base64Encode .) }}`, "vlatte")
		require.NoError(t, err)
		require.Equal(t, "dmxhdHRl vlatte", out)
	})

	t.Run("base64Decode fails on text that isn't base64", func(t *testing.T) {
		_, err := render(t, e, `{{ base64Decode "%%%" }}`, nil)
		require.Error(t, err)
	})
}
