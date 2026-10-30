package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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
		// A whole number written with a fraction or an exponent keeps every digit too: read as a float64 first,
		// these were 9007199254740992.
		{"json exponent-form integer is exact", ".json", `{"n": 9.007199254740993e15}`, map[string]any{"n": int64(9007199254740993)}},
		{"yaml exponent-form integer is exact", ".yaml", "n: 9.007199254740993e15\n", map[string]any{"n": int64(9007199254740993)}},
		{"yaml float spellings of an integer are exact", ".yaml", "a: 9_007_199_254_740_993.0\nb: +9.007199254740993E+15\nc: .5e1\n", map[string]any{
			"a": int64(9007199254740993), "b": int64(9007199254740993), "c": int64(5),
		}},
		{"yaml explicit float reads integers as yaml does", ".yaml", "a: !!float 0777\nb: !!float 0x1F\nc: !!float 9007199254740993\n", map[string]any{
			"a": int64(511), "b": int64(31), "c": int64(9007199254740993),
		}},
		{"yaml alias to an exponent-form integer is exact", ".yaml", "a: &n 9.007199254740993e15\nb: *n\n", map[string]any{
			"a": int64(9007199254740993), "b": int64(9007199254740993),
		}},
		{"yaml fraction stays a float", ".yaml", "n: !!float 2.5\n", map[string]any{"n": 2.5}},
		// A fraction is the nearest float64, so one that rounds to a whole number reads as that integer.
		{"json fraction that rounds to a whole number", ".json", `{"n": 4503599627370496.5}`, map[string]any{"n": int64(4503599627370496)}},
		{"yaml fraction that rounds to a whole number", ".yaml", "n: 4503599627370496.5\n", map[string]any{"n": int64(4503599627370496)}},
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

func TestYAMLDocuments_EachDocumentKeepsItsDataModel(t *testing.T) {
	data := []byte(`id: first
n: 9.007199254740993e15
when: 2024-01-01
body: |
  before
  ---
  after
---
id: second
value: &v true
copy: *v
200: text
`)
	documents, err := YAMLDocuments(data)
	require.NoError(t, err)
	require.Len(t, documents, 2)
	want := []map[string]any{
		{"id": "first", "n": int64(9007199254740993), "when": "2024-01-01", "body": "before\n---\nafter\n"},
		{"id": "second", "value": true, "copy": true, "200": "text"},
	}
	for i, document := range documents {
		require.Equal(t, i+1, document.Number)
		parsed, err := Parse(document.Data, "yaml")
		require.NoError(t, err)
		require.Equal(t, want[i], parsed)
	}
}

func TestYAMLDocuments_EmptyDocumentsKeepTheFollowingNumbers(t *testing.T) {
	documents, err := YAMLDocuments([]byte("---\n# empty\n---\nid: first\n---\n---\nid: second\n---\n"))
	require.NoError(t, err)
	require.Len(t, documents, 2)
	require.Equal(t, 2, documents[0].Number)
	require.Equal(t, 4, documents[1].Number)
	first, err := Parse(documents[0].Data, "yaml")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"id": "first"}, first)
	second, err := Parse(documents[1].Data, "yaml")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"id": "second"}, second)
}

func TestYAMLDocuments_EmptyStreamsHaveNoMocks(t *testing.T) {
	for _, data := range []string{"", "# empty\n", "---\n# empty\n---\n...\n"} {
		t.Run(data, func(t *testing.T) {
			documents, err := YAMLDocuments([]byte(data))
			require.NoError(t, err)
			require.Nil(t, documents)
		})
	}
}

func TestYAMLDocuments_ExplicitValuesRemainForSchemaValidation(t *testing.T) {
	for _, data := range []string{"null\n", "~\n", "!!null\n", "tea\n", "[tea]\n"} {
		t.Run(data, func(t *testing.T) {
			documents, err := YAMLDocuments([]byte(data))
			require.NoError(t, err)
			require.Equal(t, []YAMLDocument{{Number: 1, Data: []byte(data)}}, documents)
		})
	}
}

func TestYAMLDocuments_ASingleFirstDocumentKeepsItsOriginalDiagnostics(t *testing.T) {
	data := []byte("# header\n\n---\nid: first\n\n# trailing\n")
	documents, err := YAMLDocuments(data)
	require.NoError(t, err)
	require.Equal(t, []YAMLDocument{{Number: 1, Data: data}}, documents)
}

func TestYAMLDocuments_ALaterSyntaxErrorReturnsNoDocuments(t *testing.T) {
	for _, data := range []string{"id: first\n---\nbad: [\n", "---\n---\nbad: [\n"} {
		t.Run(data, func(t *testing.T) {
			documents, err := YAMLDocuments([]byte(data))
			require.ErrorContains(t, err, "yaml: document 2: yaml: line")
			require.Nil(t, documents)
		})
	}
}

func TestYAMLDocuments_AnchorsBelongToTheirOwnDocument(t *testing.T) {
	documents, err := YAMLDocuments([]byte("value: &v first\ncopy: *v\n---\nvalue: &v second\ncopy: *v\n"))
	require.NoError(t, err)
	require.Len(t, documents, 2)
	for i, want := range []string{"first", "second"} {
		parsed, err := Parse(documents[i].Data, "yaml")
		require.NoError(t, err)
		require.Equal(t, map[string]any{"value": want, "copy": want}, parsed)
	}

	documents, err = YAMLDocuments([]byte("value: &v first\n---\ncopy: *v\n"))
	require.NoError(t, err)
	require.Len(t, documents, 2)
	_, err = Parse(documents[1].Data, "yaml")
	require.ErrorContains(t, err, "unknown anchor 'v' referenced")
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

func TestEncode_YAML(t *testing.T) {
	b := encodeAs(t, map[string]any{"a": true, "b": 2.5, "c": int64(1)}, "yaml")
	require.Equal(t, "a: true\nb: 2.5\nc: 1\n", string(b))
}

func TestEncode_UnknownFormat(t *testing.T) {
	_, err := Encode(make(map[string]any), "toml")
	require.EqualError(t, err, `config: format "toml" is not JSON or YAML`)
}

func TestParse(t *testing.T) {
	// Parse reads documents for code outside this package, such as the admin API's: both formats give one data model.
	fromJSON, err := Parse([]byte(`{"a": [1, "x"]}`), "json")
	require.NoError(t, err)

	fromYAML, err := Parse([]byte("a: [1, x]\n"), ".YML")
	require.NoError(t, err)

	require.Equal(t, map[string]any{"a": []any{int64(1), "x"}}, fromJSON)
	require.Equal(t, fromJSON, fromYAML)

	_, err = Parse([]byte("a = 1"), "toml")
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
	// C4: integers are exact int64 in JSON and YAML, however they are written: with a fraction or an exponent, one
	// beyond 2^53 used to lose its last digit to float64. Other numbers keep their exact float64.
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64().Draw(t, "integer")
		w := rapid.OneOf(rapid.Int64(), rapid.Int64Range(_maxExactInt, math.MaxInt64)).Draw(t, "whole")
		f := genFraction().Draw(t, "fraction")
		text := strconv.FormatFloat(f, 'g', -1, 64)

		tenths := strconv.FormatInt(w, 10) + "0e-1"
		if w == 0 {
			tenths = "0e-1"
		}

		for _, doc := range []struct {
			ext  string
			data string
		}{
			{".json", fmt.Sprintf(`{"a": %d, "b": %d.0, "c": %s, "d": %s, "e": %s}`, n, w, text, scientific(w), tenths)},
			{".yaml", fmt.Sprintf("a: %d\nb: %d.0\nc: %s\nd: %s\ne: %s\n", n, w, text, scientific(w), tenths)},
		} {
			got := decodeAs(t, []byte(doc.data), doc.ext)
			require.Equal(t, map[string]any{"a": n, "b": w, "c": f, "d": w, "e": w}, got, doc.ext)
		}
	})
}

// scientific writes n with one digit before the point and an exponent, as in 9.007199254740993e+15.
func scientific(n int64) string {
	digits, negative := strings.CutPrefix(strconv.FormatInt(n, 10), "-")

	sign := ""
	if negative {
		sign = "-"
	}

	mantissa := digits[:1]
	if len(digits) > 1 {
		mantissa += "." + digits[1:]
	}

	return sign + mantissa + "e+" + strconv.Itoa(len(digits)-1)
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

// Generators

// _yamlTraps are strings that YAML reads as something else when unquoted: bools, nulls, numbers,
// timestamps, merge keys and indicators. Encoders must quote them, and decoding must give them back as text.
var _yamlTraps = []string{
	"yes", "no", "on", "off", "true", "False", "null", "~", "Null", "",
	"1", "-1", "0x1f", "0o17", "1_000", "1e3", ".inf", "-.Inf", ".nan", "1.5",
	"2024-01-01", "2024-01-01T10:20:30Z", "2001-12-14 21:59:43.10 -5",
	"<<", "- a", ": b", "#c", "&anchor", "*alias", "!tag", "%dir", "@at", "`tick",
	" lead", "trail ", "a: b", "a #b", "{x}", "[y]", "'q'", `"dq"`, "multi\nline", "tab\there",
}

// genText draws text, mixing YAML traps, multi-line text and arbitrary unicode.
func genText() *rapid.Generator[string] {
	return rapid.OneOf(rapid.SampledFrom(_yamlTraps), genLines(), rapid.String())
}

// genLines draws multi-line text such as indented JSON or HTML bodies: lines with leading and trailing
// spaces or tabs, blank lines and trailing line breaks, which YAML writes as blocks.
func genLines() *rapid.Generator[string] {
	line := rapid.StringMatching(`[ \t]{0,3}[a-z0-9{}\[\]":,#&*!|>%@'-]{0,6}[ \t]{0,2}`)

	return rapid.Custom(func(t *rapid.T) string {
		lines := rapid.SliceOfN(line, 2, 5).Draw(t, "lines")
		end := rapid.SampledFrom([]string{"", "\n", "\n\n"}).Draw(t, "end")

		return strings.Join(lines, "\n") + end
	})
}

// genFraction draws a finite float64 that the data model keeps as float64:
// not whole, or whole but beyond int64.
func genFraction() *rapid.Generator[float64] {
	return rapid.Float64().Filter(func(f float64) bool {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			// JSON has no Inf or NaN. TestDecode_DataModel pins YAML's .inf instead.
			return false
		}

		return f != math.Trunc(f) || f < -(1<<63) || f >= 1<<63
	})
}

// genValue draws a value of the data model decode produces, nested up to depth.
func genValue(depth int) *rapid.Generator[any] {
	gens := []*rapid.Generator[any]{
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(genText(), func(s string) any { return s }),
		rapid.Map(rapid.Int64(), func(n int64) any { return n }),
		rapid.Map(genFraction(), func(f float64) any { return f }),
	}

	if depth > 0 {
		gens = append(gens,
			rapid.Map(rapid.SliceOfN(genValue(depth-1), 0, 4), func(s []any) any { return s }),
			rapid.Map(rapid.MapOfN(genText(), genValue(depth-1), 0, 4), func(m map[string]any) any { return m }),
		)
	}

	return rapid.OneOf(gens...)
}

// requireDataModel fails unless v holds only the types of the data model.
func requireDataModel(t require.TestingT, v any) {
	switch x := v.(type) {
	case nil, bool, string, int64, float64:
	case []any:
		for _, e := range x {
			requireDataModel(t, e)
		}
	case map[string]any:
		for _, e := range x {
			requireDataModel(t, e)
		}
	default:
		require.Failf(t, "not in the data model", "%T: %v", v, v)
	}
}

// encodeAs writes v with Encode, failing the test on errors.
func encodeAs(t rapid.TB, v any, format string) []byte {
	t.Helper()

	b, err := Encode(v, format)
	if err != nil {
		t.Fatalf("encoding %v as %s: %v", v, format, err)
	}

	return b
}

// decodeAs decodes data with the extension of the format, failing the test on errors.
func decodeAs(t rapid.TB, data []byte, ext string) any {
	t.Helper()

	doc, err := decode(data, ext)
	if err != nil {
		t.Fatalf("decoding %s %q: %v", ext, data, err)
	}

	return doc
}
