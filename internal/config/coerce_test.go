package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCoerce(t *testing.T) {
	// Env and args give text, while files give decoded values: both must reach the same typed value,
	// and anything else must be rejected with a message naming the key.
	s := testSchema()

	tests := []struct {
		name    string
		key     string
		raw     any
		want    Value
		problem string
	}{
		{name: "string", key: "name", raw: "api", want: Value{Str: "api"}},
		{name: "string from a number", key: "name", raw: 8080, want: Value{Str: "8080"}},
		{name: "string from a float", key: "name", raw: 1.5, want: Value{Str: "1.5"}},
		{name: "string from a bool", key: "name", raw: true, want: Value{Str: "true"}},
		{name: "string from null", key: "name", raw: nil, problem: "name must be a string, not null"},
		{name: "string from a list", key: "name", raw: []any{"a"}, problem: "name must be a string, not a list"},

		{name: "bool", key: "debug", raw: true, want: Value{Bool: true}},
		{name: "bool from text", key: "debug", raw: "false", want: Value{Bool: false}},
		{name: "bool from 1", key: "debug", raw: "1", want: Value{Bool: true}},
		{name: "bool from bad text", key: "debug", raw: "yes", problem: `debug="yes" is not a bool`},
		{name: "bool from a number", key: "debug", raw: 1, problem: "debug must be true or false, not a number"},

		{name: "int", key: "port", raw: 80, want: Value{Int: 80}},
		{name: "int from a JSON number", key: "port", raw: float64(150), want: Value{Int: 150}},
		{name: "int from text", key: "port", raw: " 150 ", want: Value{Int: 150}},
		{name: "int from a fraction", key: "port", raw: 1.5, problem: `port="1.5" is not an integer`},
		{name: "int from bad text", key: "port", raw: "eighty", problem: `port="eighty" is not an integer`},

		{name: "choice by name", key: "log.level", raw: "info", want: Value{Int: 1}},
		{name: "choice by name in any case", key: "log.level", raw: "INFO", want: Value{Int: 1}},
		{name: "choice by number", key: "log.level", raw: 0, want: Value{Int: 0}},
		{name: "choice by number text", key: "log.level", raw: "1", want: Value{Int: 1}},
		{name: "unlisted number", key: "log.level", raw: 5, problem: `log.level="5" is not an allowed value`},
		{name: "unlisted name", key: "log.level", raw: "verbose", problem: `log.level="verbose" is not an allowed value`},

		{name: "duration in milliseconds", key: "log.timeout", raw: 1500, want: Value{Dur: 1500 * time.Millisecond}},
		{name: "duration from a JSON number", key: "log.timeout", raw: float64(250), want: Value{Dur: 250 * time.Millisecond}},
		{name: "duration in millisecond text", key: "log.timeout", raw: "1500", want: Value{Dur: 1500 * time.Millisecond}},
		{name: "go duration", key: "log.timeout", raw: "1.5s", want: Value{Dur: 1500 * time.Millisecond}},
		{name: "negative duration", key: "log.timeout", raw: "-1s", problem: `log.timeout="-1s" is negative`},
		{name: "bad duration", key: "log.timeout", raw: "10x", problem: `log.timeout="10x" is not a duration`},
		{name: "fractional milliseconds", key: "log.timeout", raw: 1.5, problem: `log.timeout="1.5" is not a duration`},

		{name: "list", key: "tags", raw: []any{"a", 1, true}, want: Value{List: []string{"a", "1", "true"}}},
		{name: "list from text", key: "tags", raw: "a, b,,c", want: Value{List: []string{"a", "b", "c"}}},
		{name: "list from one scalar", key: "tags", raw: 5, want: Value{List: []string{"5"}}},
		{name: "list of lists", key: "tags", raw: []any{[]any{"a"}}, problem: "tags must be a list or a comma-separated string, not a list"},
		{name: "list from an object", key: "tags", raw: map[string]any{"a": 1}, problem: "tags must be a list or a comma-separated string, not an object"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, p := coerce(s.byKey[tc.key], entry{key: tc.key, raw: tc.raw, origin: "file a.json"})

			if tc.problem != "" {
				require.NotNil(t, p)
				require.Equal(t, CodeInvalidValue, p.Code)
				require.Equal(t, "file a.json", p.Origin)
				require.Equal(t, tc.key, p.Key)
				require.Equal(t, tc.problem, p.Message)

				return
			}

			require.Nil(t, p)
			require.Equal(t, tc.key, v.Key)
			require.Equal(t, "file a.json", v.Origin)
			require.Equal(t, tc.want.Str, v.Str)
			require.Equal(t, tc.want.Bool, v.Bool)
			require.Equal(t, tc.want.Int, v.Int)
			require.Equal(t, tc.want.Dur, v.Dur)
			require.Equal(t, tc.want.List, v.List)
		})
	}
}

func TestCoerce_HintsListTheAllowedValues(t *testing.T) {
	// A rejected value must say what would be accepted, so the fix doesn't need the docs.
	s := testSchema()

	_, p := coerce(s.byKey["log.level"], entry{key: "log.level", raw: "verbose", origin: "env APP_LOG_LEVEL"})
	require.Equal(t, "use one of: debug (0), info (1)", p.Hint)

	_, p = coerce(s.byKey["log.timeout"], entry{key: "log.timeout", raw: "10x", origin: "env APP_LOG_TIMEOUT"})
	require.Equal(t, "use milliseconds (1500) or a Go duration (1.5s)", p.Hint)
}
