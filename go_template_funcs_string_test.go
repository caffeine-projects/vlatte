package vlatte

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// funcCase is a template that renders to want, or fails with an error that contains err.
type funcCase struct {
	name string
	text string
	data any
	want string
	err  string
}

// runFuncCases renders each case with the default engine.
func runFuncCases(t *testing.T, cases []funcCase) {
	t.Helper()

	e := MustNewGoTemplateEngine()

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

func TestStringFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "toUpper pipes a value", text: `{{ . | toUpper }}`, data: "ação", want: "AÇÃO"},
		{name: "toLower", text: `{{ toLower "MiXed" }}`, want: "mixed"},
		{name: "trimSpace", text: `[{{ trimSpace " \t a b \n" }}]`, want: "[a b]"},
		{name: "title uppercases each word and keeps the rest", text: `{{ title "hello wORLD  again" }}`, want: "Hello WORLD  Again"},
		{name: "upperFirst uppercases the first letter only", text: `{{ upperFirst "élan vital" }}`, want: "Élan vital"},
		{name: "upperFirst of an empty string", text: `[{{ upperFirst "" }}]`, want: "[]"},
		{name: "truncate counts runes and ends with an ellipsis", text: `{{ truncate 6 "ação longa" }}`, want: "açã..."},
		{name: "truncate keeps a string that fits", text: `{{ truncate 4 "abcd" }}`, want: "abcd"},
		{name: "truncate needs room for more than the ellipsis", text: `{{ truncate 3 "abcd" }}`, err: "length 3 is not more than 3"},
		{name: "replaceAll replaces every occurrence", text: `{{ . | replaceAll "-" "" }}`, data: "a-b-c", want: "abc"},
		{name: "split gives the parts", text: `{{ index (split "," "a,b,c") 1 }}`, want: "b"},
		{name: "join prints any item", text: `{{ join ", " (list "a" 1 true) }}`, want: "a, 1, true"},
		{name: "join a []string", text: `{{ split "," "x,y" | join "|" }}`, want: "x|y"},
		{name: "join a value that isn't a list", text: `{{ join "," "abc" }}`, err: "string is not a list"},
		{name: "substring counts runes", text: `{{ substring 1 3 "ação" }}`, want: "çã"},
		{name: "substring to the end", text: `{{ substring 2 -1 "ação" }}`, want: "ão"},
		{name: "substring out of range", text: `{{ substring 2 9 "ação" }}`, err: "range [2:9] is out of the 4 runes of the string"},
		{name: "slug", text: `{{ slug "  Hello, World! Ação 2 " }}`, want: "hello-world-ação-2"},
	})
}

func TestRegexpFuncs(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "regexpFind gives the first match", text: `{{ regexpFind "[0-9]+" "order 42 of 7" }}`, want: "42"},
		{name: "regexpFind without a match is empty, for default", text: `{{ regexpFind "[0-9]+" "none" | default "n/a" }}`, want: "n/a"},
		{
			name: "regexpSubmatch gives the groups", text: `{{ $m := regexpSubmatch "(\\w+)@(\\w+)" "to ana@example" }}{{ index $m 1 }} {{ index $m 2 }}`,
			want: "ana example",
		},
		{name: "regexpSubmatch without a match is nil", text: `{{ len (regexpSubmatch "x" "y") }}`, want: "0"},
		{name: "regexpReplaceAll expands groups", text: `{{ regexpReplaceAll "(\\w+)-(\\w+)" "$2-$1" "a-b c-d" }}`, want: "b-a d-c"},
		{name: "regexpMatch", text: `{{ regexpMatch "^v[0-9]$" "v2" }} {{ regexpMatch "^v[0-9]$" "v22" }}`, want: "true false"},
		{name: "an invalid pattern fails", text: `{{ regexpMatch "(" "x" }}`, err: "error calling regexpMatch: error parsing regexp"},
		{name: "regexpFind with an invalid pattern", text: `{{ regexpFind "(" "x" }}`, err: "error calling regexpFind: error parsing regexp"},
		{name: "regexpSubmatch with an invalid pattern", text: `{{ regexpSubmatch "(" "x" }}`, err: "error calling regexpSubmatch: error parsing regexp"},
		{name: "regexpReplaceAll with an invalid pattern", text: `{{ regexpReplaceAll "(" "" "x" }}`, err: "error calling regexpReplaceAll: error parsing regexp"},
	})
}

func TestContains(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "a substring", text: `{{ contains "ell" "hello" }} {{ contains "x" "hello" }}`, want: "true false"},
		{name: "a list item", text: `{{ contains "b" (list "a" "b") }}`, want: "true"},
		{
			// JSON numbers are float64: an integer literal still finds them.
			name: "a number by value, whatever its type", text: `{{ contains 2 (fromJSON "[1, 2]") }} {{ contains 3 (fromJSON "[1, 2]") }}`,
			want: "true false",
		},
		{name: "a map key", text: `{{ contains "id" (fromJSON "{\"id\": 1}") }} {{ contains "x" (fromJSON "{\"id\": 1}") }}`, want: "true false"},
		{name: "large integers by exact value", text: `{{ contains 9007199254740993 (list 9007199254740992) }}`, want: "false"},
		{name: "nil holds nothing", text: `{{ contains "a" nil }}`, want: "false"},
		{name: "a string holds only strings", text: `{{ contains 1 "a1" }}`, err: "a string holds strings, not int"},
		{name: "a value that holds nothing", text: `{{ contains 1 2 }}`, err: "int is not a string, a list or a map"},
	})
}

func TestDefault(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "nil", text: `{{ index . "missing" | default "x" }}`, data: make(map[string]any), want: "x"},
		{name: "an empty string", text: `{{ "" | default "x" }}`, want: "x"},
		{name: "an empty list", text: `{{ list | default "x" }}`, want: "x"},
		{name: "an empty map", text: `{{ fromJSON "{}" | default "x" }}`, want: "x"},
		{name: "a nil pointer", text: `{{ . | default "x" }}`, data: (*string)(nil), want: "x"},
		{name: "a value", text: `{{ "v" | default "x" }}`, want: "v"},
		{
			// A real zero is a value: a count of 0 must not become the fallback.
			name: "zero and false are values", text: `{{ 0 | default 9 }} {{ false | default true }}`, want: "0 false",
		},
		{name: "the value keeps its type", text: `{{ len (fromJSON "[1, 2]" | default "x") }}`, want: "2"},
	})
}
