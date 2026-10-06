package config

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// coercion is a row of the coercion tables: a raw value, and either the value it converts to or its problems.
type coercion struct {
	name     string
	key      string
	raw      any
	want     Value
	problems []Problem // Code, Key, Message and Hint; Origin is the row's source
}

func runCoercions(t *testing.T, text bool, origin string, tests []coercion) {
	t.Helper()

	s := testSchema()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, problems := coerce(s.byKey[tc.key], entry{key: tc.key, raw: tc.raw, text: text, origin: origin}, tc.key)

			if len(tc.problems) > 0 {
				for i := range tc.problems {
					tc.problems[i].Code = CodeInvalidValue
					tc.problems[i].Origin = origin
				}

				for i := range problems {
					problems[i].Value = ""
				}

				require.Equal(t, tc.problems, problems)

				return
			}

			require.Empty(t, problems)
			require.Equal(t, tc.key, v.Key)
			require.Equal(t, origin, v.Origin)
			require.Equal(t, tc.want.Str, v.Str)
			require.Equal(t, tc.want.Bool, v.Bool)
			require.Equal(t, tc.want.Int, v.Int)
			require.Equal(t, tc.want.Dur, v.Dur)
			require.Equal(t, tc.want.List, v.List)
		})
	}
}

func problem(key, message, hint string) []Problem {
	return []Problem{{Key: key, Message: message, Hint: hint}}
}

func TestCoerce_Documents(t *testing.T) {
	// A value in a document must already have the type of its kind, as the editor schema says: nothing is converted,
	// except that bools read as strconv.ParseBool reads them. Each rejection says what to write instead.
	runCoercions(t, false, "file a.yaml", []coercion{
		{name: "string", key: "name", raw: "api", want: Value{Str: "api"}},
		{name: "number for a string", key: "name", raw: int64(8080), problems: problem("name", "name must be a string, not a number", `write it in quotes: "8080"`)},
		{name: "fraction for a string", key: "name", raw: 1.5, problems: problem("name", "name must be a string, not a number", `write it in quotes: "1.5"`)},
		{name: "bool for a string", key: "name", raw: true, problems: problem("name", "name must be a string, not a bool", `write it in quotes: "true"`)},
		{name: "null for a string", key: "name", raw: nil, problems: problem("name", "name must be a string, not null", "")},
		{name: "list for a string", key: "name", raw: []any{"a"}, problems: problem("name", "name must be a string, not a list", "")},
		{name: "object for a string", key: "name", raw: map[string]any{"a": "b"}, problems: problem("name", "name must be a string, not an object", "")},

		{name: "bool", key: "debug", raw: true, want: Value{Bool: true}},
		{name: "bool text", key: "debug", raw: "false", want: Value{Bool: false}},
		{name: "ParseBool text", key: "debug", raw: "t", want: Value{Bool: true}},
		{name: "1 is true", key: "debug", raw: int64(1), want: Value{Bool: true}},
		{name: "0 is false", key: "debug", raw: int64(0), want: Value{Bool: false}},
		{name: "text ParseBool can't read", key: "debug", raw: "yes", problems: problem("debug", `debug must be true or false, not "yes"`, "")},
		{name: "number other than 1 or 0", key: "debug", raw: int64(2), problems: problem("debug", "debug must be true or false, not 2", "")},
		{name: "null for a bool", key: "debug", raw: nil, problems: problem("debug", "debug must be true or false, not null", "")},
		{name: "list for a bool", key: "debug", raw: []any{}, problems: problem("debug", "debug must be true or false, not a list", "")},

		{name: "integer", key: "port", raw: int64(80), want: Value{Int: 80}},
		{name: "fraction", key: "port", raw: 1.5, problems: problem("port", "port must be an integer, not 1.5", "")},
		{
			name: "integer beyond int64", key: "port", raw: 1e20,
			problems: problem("port", "port must be an integer, not 100000000000000000000", "integers go from -9223372036854775808 to 9223372036854775807"),
		},
		{name: "integer in quotes", key: "port", raw: "150", problems: problem("port", "port must be an integer, not a string", "write it without quotes: 150")},
		{name: "text for an integer", key: "port", raw: "eighty", problems: problem("port", "port must be an integer, not a string", "")},
		{name: "bool for an integer", key: "port", raw: true, problems: problem("port", "port must be an integer, not a bool", "")},

		{name: "choice", key: "log.level", raw: "info", want: Value{Int: 1}},
		{name: "choice in another case", key: "log.level", raw: "INFO", want: Value{Int: 1}},
		{name: "choice capitalized", key: "log.level", raw: "Debug", want: Value{Int: 0}},
		{
			name: "number of a choice", key: "log.level", raw: int64(1),
			problems: problem("log.level", "log.level must be one of debug or info, not a number", "write the name: info"),
		},
		{name: "number of no choice", key: "log.level", raw: int64(5), problems: problem("log.level", "log.level must be one of debug or info, not a number", "")},
		{name: "unknown choice", key: "log.level", raw: "verbose", problems: problem("log.level", `log.level must be one of debug or info, not "verbose"`, "")},
		{name: "misspelled choice", key: "log.level", raw: "inf", problems: problem("log.level", `log.level must be one of debug or info, not "inf"`, "did you mean info?")},
		{name: "choice with spaces", key: "log.level", raw: " info", problems: problem("log.level", `log.level must be one of debug or info, not " info"`, "did you mean info?")},
		{name: "number of a choice in quotes", key: "log.level", raw: "1", problems: problem("log.level", `log.level must be one of debug or info, not "1"`, "write the name: info")},

		{name: "milliseconds", key: "log.timeout", raw: int64(1500), want: Value{Dur: 1500 * time.Millisecond}},
		{name: "go duration", key: "log.timeout", raw: "1.5s", want: Value{Dur: 1500 * time.Millisecond}},
		{name: "zero", key: "log.timeout", raw: "0", want: Value{Dur: 0}},
		{name: "negative milliseconds", key: "log.timeout", raw: int64(-5), problems: problem("log.timeout", "log.timeout must be 0 or more, not -5", "")},
		{name: "negative duration", key: "log.timeout", raw: "-1s", problems: problem("log.timeout", `log.timeout must be 0 or more, not "-1s"`, "")},
		{
			name: "signed duration", key: "log.timeout", raw: "+1s",
			problems: problem("log.timeout", `log.timeout must be milliseconds or a Go duration, not "+1s"`, "such as 1500 or 1.5s"),
		},
		{
			name: "milliseconds in quotes", key: "log.timeout", raw: "1500",
			problems: problem("log.timeout", `log.timeout must be milliseconds or a Go duration, not "1500"`,
				`write 1500 without quotes for milliseconds, or add a unit, such as "1500ms"`),
		},
		{
			name: "bad duration", key: "log.timeout", raw: "10x",
			problems: problem("log.timeout", `log.timeout must be milliseconds or a Go duration, not "10x"`, "such as 1500 or 1.5s"),
		},
		{
			name: "fractional milliseconds", key: "log.timeout", raw: 1.5,
			problems: problem("log.timeout", "log.timeout must be whole milliseconds or a Go duration, not 1.5", `such as "1.5ms"`),
		},
		{
			name: "milliseconds beyond a duration", key: "log.timeout", raw: _maxMilliseconds + 1,
			problems: problem("log.timeout", "log.timeout must be at most 9223372036854 milliseconds, not 9223372036855", ""),
		},
		{name: "bool for a duration", key: "log.timeout", raw: true, problems: problem("log.timeout", "log.timeout must be milliseconds or a Go duration, not a bool", "")},

		{name: "list", key: "tags", raw: []any{"a", "b"}, want: Value{List: []string{"a", "b"}}},
		{name: "empty list", key: "tags", raw: []any{}, want: Value{List: []string{}}},
		{name: "items kept as written", key: "tags", raw: []any{" a", ""}, want: Value{List: []string{" a", ""}}},
		{
			name: "items that aren't strings", key: "tags", raw: []any{"a", int64(1), true},
			problems: []Problem{
				{Key: "tags[1]", Message: "tags[1] must be a string, not a number", Hint: `write it in quotes: "1"`},
				{Key: "tags[2]", Message: "tags[2] must be a string, not a bool", Hint: `write it in quotes: "true"`},
			},
		},
		{name: "list in a list", key: "tags", raw: []any{[]any{"a"}}, problems: problem("tags[0]", "tags[0] must be a string, not a list", "")},
		{name: "text for a list", key: "tags", raw: "a, b", problems: problem("tags", "tags must be a list of strings, not a string", `write a list: ["a", "b"]`)},
		{name: "number for a list", key: "tags", raw: int64(5), problems: problem("tags", "tags must be a list of strings, not a number", "")},
	})
}

func TestCoerce_Text(t *testing.T) {
	// Env and args give text, read with the same rules as everywhere: bools as strconv.ParseBool reads them,
	// numbers in the JSON grammar, durations as milliseconds or Go durations, lists comma-separated.
	runCoercions(t, true, "env APP_X", []coercion{
		{name: "text as written", key: "name", raw: " 8080 ", want: Value{Str: " 8080 "}},

		{name: "ParseBool 1", key: "debug", raw: "1", want: Value{Bool: true}},
		{name: "ParseBool t", key: "debug", raw: "t", want: Value{Bool: true}},
		{name: "ParseBool FALSE", key: "debug", raw: "FALSE", want: Value{Bool: false}},
		{name: "not a bool", key: "debug", raw: "yes", problems: problem("debug", `debug must be true or false, not "yes"`, "")},
		{name: "bool with spaces", key: "debug", raw: " true", problems: problem("debug", `debug must be true or false, not " true"`, "")},

		{name: "integer", key: "port", raw: "150", want: Value{Int: 150}},
		{name: "whole fraction", key: "port", raw: "5.0", want: Value{Int: 5}},
		{name: "exponent", key: "port", raw: "1e2", want: Value{Int: 100}},
		{name: "minus zero", key: "port", raw: "-0", want: Value{Int: 0}},
		{name: "plus sign", key: "port", raw: "+5", problems: problem("port", `port must be an integer, not "+5"`, "")},
		{name: "leading zero", key: "port", raw: "05", problems: problem("port", `port must be an integer, not "05"`, "")},
		{name: "spaces", key: "port", raw: " 5", problems: problem("port", `port must be an integer, not " 5"`, "")},
		{name: "fraction", key: "port", raw: "1.5", problems: problem("port", `port must be an integer, not "1.5"`, "")},
		{name: "words", key: "port", raw: "eighty", problems: problem("port", `port must be an integer, not "eighty"`, "")},
		{
			name: "integer beyond int64", key: "port", raw: "1e19",
			problems: problem("port", `port must be an integer, not "1e19"`, "integers go from -9223372036854775808 to 9223372036854775807"),
		},

		{name: "choice", key: "log.level", raw: "info", want: Value{Int: 1}},
		{name: "choice in capitals", key: "log.level", raw: "INFO", want: Value{Int: 1}},
		{name: "number of a choice", key: "log.level", raw: "1", problems: problem("log.level", `log.level must be one of debug or info, not "1"`, "write the name: info")},
		{name: "unknown choice", key: "log.level", raw: "verbose", problems: problem("log.level", `log.level must be one of debug or info, not "verbose"`, "")},

		{name: "milliseconds", key: "log.timeout", raw: "1500", want: Value{Dur: 1500 * time.Millisecond}},
		{name: "go duration", key: "log.timeout", raw: "1.5s", want: Value{Dur: 1500 * time.Millisecond}},
		{name: "zero", key: "log.timeout", raw: "0", want: Value{Dur: 0}},
		{name: "negative milliseconds", key: "log.timeout", raw: "-5", problems: problem("log.timeout", `log.timeout must be 0 or more, not "-5"`, "")},
		{
			name: "fractional milliseconds", key: "log.timeout", raw: "1.5",
			problems: problem("log.timeout", `log.timeout must be whole milliseconds or a Go duration, not "1.5"`, `such as "1.5ms"`),
		},
		{name: "negative duration", key: "log.timeout", raw: "-1s", problems: problem("log.timeout", `log.timeout must be 0 or more, not "-1s"`, "")},
		{
			name: "bad duration", key: "log.timeout", raw: "abc",
			problems: problem("log.timeout", `log.timeout must be milliseconds or a Go duration, not "abc"`, "such as 1500 or 1.5s"),
		},

		{name: "list", key: "tags", raw: "a, b,,c", want: Value{List: []string{"a", "b", "c"}}},
		{name: "empty list", key: "tags", raw: "", want: Value{List: []string{}}},
	})
}

func TestCoerce_AnyKeepsTheValue(t *testing.T) {
	// An Any setting gets the value as decoded, or the text, for its Set to decode.
	f := &field{key: "x", kind: Any}

	v, problems := coerce(f, entry{key: "x", raw: []any{int64(1)}, origin: "file a.json"}, "x")
	require.Empty(t, problems)
	require.Equal(t, []any{int64(1)}, v.Any)

	v, problems = coerce(f, entry{key: "x", raw: "1", text: true, origin: "env APP_X"}, "x")
	require.Empty(t, problems)
	require.Equal(t, "1", v.Any)
}

func TestMismatch(t *testing.T) {
	// Every wrong type is worded alike, wherever it is found, and the hint follows the kinds a setting takes.
	tests := []struct {
		raw   any
		want  string
		takes []Kind
		msg   string
		hint  string
	}{
		{int64(5), "a string or a list of strings", []Kind{String}, "h.x must be a string or a list of strings, not a number", `write it in quotes: "5"`},
		{true, "a string", []Kind{String}, "h.x must be a string, not a bool", `write it in quotes: "true"`},
		{"5", "an integer", []Kind{Int}, "h.x must be an integer, not a string", "write it without quotes: 5"},
		{"5.5", "an integer", []Kind{Int}, "h.x must be an integer, not a string", ""},
		{"a,b", "a list", []Kind{List}, "h.x must be a list, not a string", `write a list: ["a", "b"]`},
		{int64(5), "an object", nil, "h.x must be an object, not a number", ""},
		{nil, "an object", []Kind{String}, "h.x must be an object, not null", ""},
	}

	for _, tc := range tests {
		err := Mismatch(Value{Key: "h.x", Origin: "file m.yaml", Raw: rawText(tc.raw), Any: tc.raw}, tc.want, tc.takes...)

		cfgErr, ok := errors.AsType[*Error](err)
		require.True(t, ok)
		require.Equal(t, []Problem{{
			Code: CodeInvalidValue, Origin: "file m.yaml", Key: "h.x", Value: rawText(tc.raw), Message: tc.msg, Hint: tc.hint,
		}}, cfgErr.Problems, strconv.Quote(tc.msg))
	}
}

func TestWant_EveryKind(t *testing.T) {
	// One word for what each kind takes, used by every rejection of the kind.
	for kind, want := range map[Kind]string{
		String: "a string", Bool: "true or false", Int: "an integer", Duration: "milliseconds or a Go duration",
		List: "a list of strings", Any: "a value",
	} {
		require.Equal(t, want, wantOf(kind), kind.String())
	}
}

// wantOf is what want says of a field of kind, without choices.
func wantOf(kind Kind) string {
	return want(&field{kind: kind})
}

func TestOrList(t *testing.T) {
	// Choices are listed as people write lists, whatever their number.
	require.Empty(t, orList(nil))
	require.Equal(t, "info", orList([]string{"info"}))
	require.Equal(t, "debug or info", orList([]string{"debug", "info"}))
	require.Equal(t, "trace, debug or info", orList([]string{"trace", "debug", "info"}))
}
