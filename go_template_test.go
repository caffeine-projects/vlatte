package vlatte

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestGoTemplateFuncs(t *testing.T) {
	e := MustNewGoTemplateEngine()

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
