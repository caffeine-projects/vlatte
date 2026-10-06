package config

import (
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestDecode_DataModel(t *testing.T) {
	// JSON and YAML decode into one data model, so a document means the same in both formats.
	tests := []struct {
		name string
		ext  string
		doc  string
		want any
	}{
		{"json whole number", ".json", `{"n": 5}`, map[string]any{"n": int64(5)}},
		{"json whole number with a fraction part", ".json", `{"n": 5.0}`, map[string]any{"n": int64(5)}},
		{"json fraction", ".json", `{"n": 2550.5}`, map[string]any{"n": 2550.5}},
		{"yaml int", ".yaml", "n: 5\n", map[string]any{"n": int64(5)}},
		{"yaml whole float", ".yaml", "n: 5.0\n", map[string]any{"n": int64(5)}},
		{"yaml fraction", ".yml", "n: 2550.50\n", map[string]any{"n": 2550.5}},
		{"yaml timestamp is text", ".yaml", "d: 2024-01-01\n", map[string]any{"d": "2024-01-01"}},
		{"yaml timestamp with time is text", ".yaml", "d: 2001-12-14t21:59:43.10-05:00\n", map[string]any{"d": "2001-12-14t21:59:43.10-05:00"}},
		{"yaml timestamp in a list is text", ".yaml", "- 2024-01-01\n", []any{"2024-01-01"}},
		{"yaml number key is text", ".yaml", "200: ok\n", map[string]any{"200": "ok"}},
		{"yaml bool and null keys are text", ".yaml", "true: a\n~: b\n", map[string]any{"true": "a", "~": "b"}},
		{"yaml key keeps its exact text", ".yaml", "0x1F: a\n1_000: b\n", map[string]any{"0x1F": "a", "1_000": "b"}},
		{"yaml alias to a timestamp is text", ".yaml", "a: &d 2024-01-01\nb: *d\n", map[string]any{"a": "2024-01-01", "b": "2024-01-01"}},
		{"yaml alias to text as a key", ".yaml", "a: &k name\n*k : v\n", map[string]any{"a": "name", "name": "v"}},
		{
			"yaml merge key keeps its meaning", ".yaml", "base: &b {x: 1}\nchild:\n  <<: *b\n  y: 2\n",
			map[string]any{"base": map[string]any{"x": int64(1)}, "child": map[string]any{"x": int64(1), "y": int64(2)}},
		},
		{"yaml quoted merge key is text", ".yaml", "\"<<\": a\n", map[string]any{"<<": "a"}},
		{"yaml infinity stays a float", ".yaml", "n: .inf\n", map[string]any{"n": math.Inf(1)}},
		{"yaml integer beyond 2^53 stays exact", ".yaml", "n: 9007199254740993\n", map[string]any{"n": int64(9007199254740993)}},
		// JSON integers are read as integers, so they keep every digit, as YAML integers do.
		{"json integer beyond 2^53 is exact", ".json", `{"n": 10000000000000001}`, map[string]any{"n": int64(10000000000000001)}},
		{"json integer beyond int64 is a float", ".json", `{"n": 100000000000000000000}`, map[string]any{"n": 1e20}},
		{"yaml integer beyond int64 is a float", ".yaml", "n: 100000000000000000000\n", map[string]any{"n": 1e20}},
		{"json whole number written as a float", ".json", `{"n": 1e16}`, map[string]any{"n": int64(1e16)}},
		{"yaml uint beyond int64 is a float", ".yaml", "n: 18446744073709551615\n", map[string]any{"n": float64(math.MaxUint64)}},
		{"empty json", ".json", " \n", nil},
		{"empty yaml", ".yaml", "", nil},
		{"comments only", ".yaml", "# nothing\n", nil},
		{"null document", ".yaml", "null\n", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, decodeAs(t, []byte(tc.doc), tc.ext))
		})
	}
}

func TestDecode_Errors(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		doc  string
		want string
	}{
		{"json syntax", ".json", `{"a": }`, "jsontext: missing value after object name"},
		{"json with two values", ".json", `{} {}`, "a document holds one value, not more"},
		{"json number out of range", ".json", `{"n": 1e400}`, "number 1e400 is out of range"},
		{"json duplicate names", ".json", `{"a": 1, "a": 2}`, `duplicate object member name "a"`},
		{"json syntax in a list", ".json", `[1, nul]`, `invalid character ']' in literal null`},
		{"json syntax in an object in a list", ".json", `[{"a": }]`, "jsontext: missing value after object name"},
		{"yaml syntax", ".yaml", "a: [1, 2\n", "yaml: line"},
		{"yaml duplicate keys", ".yaml", "a: 1\na: 2\n", `mapping key "a" already defined`},
		{"yaml list as a key", ".yaml", "? [a, b]\n: c\n", "yaml: line 1: a mapping key must be text"},
		{"yaml list as a key of a nested object", ".yaml", "a:\n  b: {[1]: x}\n", "yaml: line 2: a mapping key must be text"},
		{"yaml alias to a number as a key", ".yaml", "a: &n 1\n*n : b\n", "a mapping key must be text"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decode([]byte(tc.doc), tc.ext)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestPropDecode_FormatsAgree(t *testing.T) {
	// C1: a value written as JSON and as YAML decodes back to itself in both formats.
	rapid.Check(t, func(t *rapid.T) {
		v := genValue(3).Draw(t, "value")

		require.Equal(t, v, decodeAs(t, encodeAs(t, v, "json"), ".json"), "json")
		require.Equal(t, v, decodeAs(t, encodeAs(t, v, "yaml"), ".yaml"), "yaml")
	})
}

func TestEncode_MergeKeyIsQuoted(t *testing.T) {
	// The YAML encoder writes a "<<" key plain, and a plain << reads back as a merge key, which fails to decode.
	// Found by TestPropDecode_FormatsAgree: []any{map[string]any{"<<": nil}}.
	v := []any{map[string]any{"<<": nil}}
	b := encodeAs(t, v, "yaml")

	require.Equal(t, "- \"<<\": null\n", string(b))
	require.Equal(t, v, decodeAs(t, b, ".yaml"))
}

func TestEncode_TextStyles(t *testing.T) {
	// Found by TestPropDecode_FormatsAgree: the YAML encoder writes text it can't read back
	// when a block starts with a line break or a tab. Readable bodies, such as indented JSON, stay blocks.
	tests := []struct {
		name string
		text string
		want string
	}{
		{"one line", "plain", "plain\n"},
		{"indented json body is a block", "{\n  \"a\": 1\n}\n", "|\n    {\n      \"a\": 1\n    }\n"},
		{"leading line break is quoted", "\nA", "\"\\nA\"\n"},
		{"leading tab is quoted", "\t\n", "\"\\t\\n\"\n"},
		{"leading space is quoted", " a\nb", "\" a\\nb\"\n"},
		{"control character is quoted", "a\x01\nb", "\"a\\x01\\nb\"\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := encodeAs(t, tc.text, "yaml")

			require.Equal(t, tc.want, string(b))
			require.Equal(t, tc.text, decodeAs(t, b, ".yaml"))
		})
	}
}

func TestEncode_JSON(t *testing.T) {
	b := encodeAs(t, map[string]any{"b": []any{int64(1), 2.5}, "a": "x"}, ".JSON")
	require.Equal(t, "{\n  \"a\": \"x\",\n  \"b\": [\n    1,\n    2.5\n  ]\n}\n", string(b))
}

func TestEncode_UnknownFormat(t *testing.T) {
	_, err := Encode(make(map[string]any), "toml")
	require.EqualError(t, err, `config: format "toml" is not JSON or YAML`)
}

func TestPropDecode_TimestampsAndKeysStayText(t *testing.T) {
	// C3: unquoted YAML timestamps and scalar keys keep their exact text, wherever they are.
	timestamp := rapid.Custom(func(t *rapid.T) string {
		date := fmt.Sprintf("%04d-%02d-%02d",
			rapid.IntRange(1, 9999).Draw(t, "year"), rapid.IntRange(1, 12).Draw(t, "month"), rapid.IntRange(1, 28).Draw(t, "day"))

		if !rapid.Bool().Draw(t, "with time") {
			return date
		}

		sep := rapid.SampledFrom([]string{"T", "t", " "}).Draw(t, "separator")
		clock := fmt.Sprintf("%02d:%02d:%02d", rapid.IntRange(0, 23).Draw(t, "hour"),
			rapid.IntRange(0, 59).Draw(t, "minute"), rapid.IntRange(0, 59).Draw(t, "second"))
		zone := rapid.SampledFrom([]string{"", "Z", "+01:00", "-05:30"}).Draw(t, "zone")

		return date + sep + clock + zone
	})

	scalarKey := rapid.OneOf(
		timestamp,
		rapid.Map(rapid.Int64(), func(n int64) string { return strconv.FormatInt(n, 10) }),
		rapid.SampledFrom([]string{"true", "false", "null", "~", "1.5", "0x1F", "1_000", ".inf"}),
	)

	rapid.Check(t, func(t *rapid.T) {
		ts := timestamp.Draw(t, "timestamp")
		key := scalarKey.Draw(t, "key")
		doc := fmt.Sprintf("v: %s\nl: [%s]\n%s: k\n", ts, ts, key)

		want := map[string]any{"v": ts, "l": []any{ts}, key: "k"}
		require.Equal(t, want, decodeAs(t, []byte(doc), ".yaml"))
	})
}

func TestPropDecode_WholeNumbersAreInt64(t *testing.T) {
	// C4: integers are exact int64 in JSON and YAML; a whole number written with a fraction is an int64 too,
	// when its float64 is exact; other numbers keep their exact float64.
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64().Draw(t, "integer")
		w := rapid.Int64Range(-_maxExactInt, _maxExactInt).Draw(t, "whole")
		f := genFraction().Draw(t, "fraction")
		text := strconv.FormatFloat(f, 'g', -1, 64)

		for _, doc := range []struct {
			ext  string
			data string
		}{
			{".json", fmt.Sprintf(`{"a": %d, "b": %d.0, "c": %s}`, n, w, text)},
			{".yaml", fmt.Sprintf("a: %d\nb: %d.0\nc: %s\n", n, w, text)},
		} {
			got := decodeAs(t, []byte(doc.data), doc.ext)
			require.Equal(t, map[string]any{"a": n, "b": w, "c": f}, got, doc.ext)
		}
	})
}

func FuzzDecode(f *testing.F) {
	// C2 and C10: no input panics, and whatever decodes is in the data model.
	for _, seed := range []string{
		`{"a": [1, 2.5, "x", null, true, {"b": {}}]}`,
		"a: &x [1, 2]\nb: *x\nc:\n  <<: {d: 2024-01-01}\n",
		"? [a, b]\n: c\n",
		"200: ok\n1.5: x\ntrue: y\n~: z\n",
		"n: 18446744073709551615\nm: .nan\n",
		"- - - - - []\n",
		"",
		"{",
	} {
		f.Add([]byte(seed), true)
		f.Add([]byte(seed), false)
	}

	f.Fuzz(func(t *testing.T, data []byte, asYAML bool) {
		ext := ".json"
		if asYAML {
			ext = ".yaml"
		}

		doc, err := decode(data, ext)
		if err != nil {
			return
		}

		requireDataModel(t, doc)

		// The schema walk never panics either, and every problem says what and where.
		_, problems, err := walkDoc(&testSchema().catalog, data, ext, "fuzz", "", "a document")
		require.NoError(t, err)

		for _, p := range problems {
			require.NotEmpty(t, p.Code)
			require.NotEmpty(t, p.Origin)
			require.NotEmpty(t, p.Message)
		}
	})
}

func TestEncode_FloatsAndErrors(t *testing.T) {
	// YAML can hold the floats JSON can't, and both refuse what isn't a value of a document.
	b := encodeAs(t, []any{math.Inf(1), math.Inf(-1), math.NaN()}, "yaml")
	require.Equal(t, "- .inf\n- -.inf\n- .nan\n", string(b))

	got, ok := decodeAs(t, b, ".yaml").([]any)
	require.True(t, ok)
	require.Equal(t, math.Inf(1), got[0])
	require.Equal(t, math.Inf(-1), got[1])
	require.True(t, math.IsNaN(got[2].(float64)))

	_, err := Encode(map[string]any{"a": []any{int32(1)}}, "yaml")
	require.EqualError(t, err, "config: int32 is not a value of a document")

	_, err = Encode(map[string]any{"a": math.NaN()}, "json")
	require.Error(t, err, "JSON has no NaN")
}
