package vlatte

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const _orderJSON = `{"id": "o-1", "items": [{"sku": "a", "qty": 2}, {"sku": "b", "qty": 1}], "note": null}`

func TestJSONPath(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "reads JSON text, such as a request body", text: `{{ jsonPath "$.items[1].sku" . }}`, data: _orderJSON, want: "b"},
		{name: "reads a value", text: `{{ jsonPath "$.id" (fromJSON .) }}`, data: _orderJSON, want: "o-1"},
		{name: "reads JSON bytes", text: `{{ jsonPath "$.id" . }}`, data: []byte(_orderJSON), want: "o-1"},
		{name: "gives the first of many", text: `{{ jsonPath "$.items[*].sku" . }}`, data: _orderJSON, want: "a"},
		{name: "keeps the type of what it selects", text: `{{ jsonPath "$.items[0]" . | toJSON }}`, data: _orderJSON, want: `{"qty":2,"sku":"a"}`},
		{name: "nothing selected is nil, for default", text: `{{ jsonPath "$.missing" . | default "none" }}`, data: _orderJSON, want: "none"},
		{name: "jsonPathAll gives every value", text: `{{ jsonPathAll "$.items[*].qty" . | toJSON }}`, data: _orderJSON, want: "[2,1]"},
		{name: "jsonPathAll of nothing is an empty list", text: `{{ jsonPathAll "$.missing" . | toJSON }}`, data: _orderJSON, want: "[]"},
		{name: "text that isn't JSON", text: `{{ jsonPath "$.id" "{" }}`, err: "error calling jsonPath: jsontext: unexpected EOF"},
		{name: "an invalid path", text: `{{ jsonPath "$[" "{}" }}`, err: "error calling jsonPath: not terminated"},
	})
}

func TestDeleteJSONPath(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "removes a key", text: `{{ deleteJSONPath "$.note" (fromJSON .) | toJSON }}`, data: `{"id": 1, "note": "x"}`, want: `{"id":1}`},
		{name: "removes list items", text: `{{ deleteJSONPath "$.items[0]" (fromJSON .) | toJSON }}`, data: `{"items": [1, 2]}`, want: `{"items":[2]}`},
		{name: "reads JSON text, such as a request body", text: `{{ deleteJSONPath "$.a" . | toJSON }}`, data: `{"a": 1, "b": 2}`, want: `{"b":2}`},
		{name: "nothing selected changes nothing", text: `{{ deleteJSONPath "$.x" (fromJSON .) | toJSON }}`, data: `{"id": 1}`, want: `{"id":1}`},
		{
			// null is a value: a key that holds it stays unless the path selects it.
			name: "keeps nulls it doesn't select", text: `{{ deleteJSONPath "$.d" . | toJSON }}`,
			data: `{"a": {"b": null, "c": 1}, "d": 2, "e": [null]}`, want: `{"a":{"b":null,"c":1},"e":[null]}`,
		},
		{name: "an invalid path", text: `{{ deleteJSONPath "$[" "{}" }}`, err: "error calling deleteJSONPath: not terminated"},
		{name: "text that isn't JSON", text: `{{ deleteJSONPath "$.a" "{" }}`, err: "error calling deleteJSONPath: jsontext: unexpected EOF"},
	})

	t.Run("keeps the values it doesn't select as they are", func(t *testing.T) {
		data := map[string]any{"v": map[string]any{"n": 1, "z": nil, "x": "y"}}

		out, err := render(t, MustNewGoTemplateEngine(), `{{ $r := deleteJSONPath "$.x" .v }}{{ printf "%T" $r.n }} {{ len $r }}`, data)
		require.NoError(t, err)
		require.Equal(t, "int 2", out)
	})

	t.Run("leaves its value unchanged", func(t *testing.T) {
		data := map[string]any{"order": map[string]any{"id": "1", "note": "x"}}

		out, err := render(t, MustNewGoTemplateEngine(), `{{ deleteJSONPath "$.note" .order | toJSON }}`, data)
		require.NoError(t, err)
		require.JSONEq(t, `{"id": "1"}`, out)
		require.Equal(t, map[string]any{"id": "1", "note": "x"}, data["order"])
	})
}

func TestMergePatch(t *testing.T) {
	// The examples of RFC 7386, appendix A, as JSON text.
	rfc := []struct {
		target string
		patch  string
		want   string
	}{
		{target: `{"a":"b"}`, patch: `{"a":"c"}`, want: `{"a":"c"}`},
		{target: `{"a":"b"}`, patch: `{"b":"c"}`, want: `{"a":"b","b":"c"}`},
		{target: `{"a":"b"}`, patch: `{"a":null}`, want: `{}`},
		{target: `{"a":"b","b":"c"}`, patch: `{"a":null}`, want: `{"b":"c"}`},
		{target: `{"a":["b"]}`, patch: `{"a":"c"}`, want: `{"a":"c"}`},
		{target: `{"a":"c"}`, patch: `{"a":["b"]}`, want: `{"a":["b"]}`},
		{target: `{"a":{"b":"c"}}`, patch: `{"a":{"b":"d","c":null}}`, want: `{"a":{"b":"d"}}`},
		{target: `{"a":[{"b":"c"}]}`, patch: `{"a":[1]}`, want: `{"a":[1]}`},
		{target: `["a","b"]`, patch: `["c","d"]`, want: `["c","d"]`},
		{target: `{"a":"b"}`, patch: `["c"]`, want: `["c"]`},
		{target: `{"a":"foo"}`, patch: `null`, want: `null`},
		{target: `{"a":"foo"}`, patch: `"bar"`, want: `"bar"`},
		{target: `{"e":null}`, patch: `{"a":1}`, want: `{"a":1,"e":null}`},
		{target: `[1,2]`, patch: `{"a":"b","c":null}`, want: `{"a":"b"}`},
		{target: `{}`, patch: `{"a":{"bb":{"ccc":null}}}`, want: `{"a":{"bb":{}}}`},
	}

	e := MustNewGoTemplateEngine()

	for _, tc := range rfc {
		t.Run(tc.target+" patched with "+tc.patch, func(t *testing.T) {
			out, err := render(t, e, `{{ mergePatch .patch .target | toJSON }}`,
				map[string]any{"target": tc.target, "patch": tc.patch})
			require.NoError(t, err)
			require.JSONEq(t, tc.want, out)
		})
	}

	t.Run("text that isn't JSON", func(t *testing.T) {
		_, err := render(t, e, `{{ mergePatch "{" "{}" }}`, nil)
		require.ErrorContains(t, err, "error calling mergePatch: jsontext: unexpected EOF")

		_, err = render(t, e, `{{ mergePatch "{}" "{" }}`, nil)
		require.ErrorContains(t, err, "error calling mergePatch: jsontext: unexpected EOF")
	})

	t.Run("patches a map of any value type, such as query parameters", func(t *testing.T) {
		data := map[string]any{"query": map[string]string{"page": "1", "sort": "asc"}}

		out, err := render(t, e, `{{ mergePatch "{\"sort\": null, \"size\": 10}" .query | toJSON }}`, data)
		require.NoError(t, err)
		require.JSONEq(t, `{"page": "1", "size": 10}`, out)
	})

	t.Run("leaves its value unchanged", func(t *testing.T) {
		data := map[string]any{"user": map[string]any{"name": "ana", "address": map[string]any{"city": "x"}}}

		out, err := render(t, e, `{{ mergePatch (fromJSON "{\"address\": {\"city\": \"y\"}}") .user | toJSON }}`, data)
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "ana", "address": {"city": "y"}}`, out)
		require.Equal(t, map[string]any{"name": "ana", "address": map[string]any{"city": "x"}}, data["user"])
	})
}

func TestJSONFormatting(t *testing.T) {
	runFuncCases(t, []funcCase{
		{
			// Keys keep their order, which toJSON (fromJSON ...) would sort.
			name: "indentJSON", text: `{{ indentJSON . }}`, data: `{"b": 1, "a": [1, 2]}`,
			want: "{\n  \"b\": 1,\n  \"a\": [\n    1,\n    2\n  ]\n}",
		},
		{name: "compactJSON", text: `{{ compactJSON . }}`, data: "{\n  \"b\": 1,\n  \"a\": [1, 2]\n}", want: `{"b":1,"a":[1,2]}`},
		{name: "indentJSON of text that isn't JSON", text: `{{ indentJSON "{" }}`, err: "error calling indentJSON: jsontext: unexpected EOF"},
		{name: "compactJSON of text that isn't JSON", text: `{{ compactJSON "{" }}`, err: "error calling compactJSON: jsontext: unexpected EOF"},
	})
}
