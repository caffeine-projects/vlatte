package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func loadBytes(t *testing.T, origin, format, doc string) (testConfig, map[string]Value, error) {
	t.Helper()

	return loadFile(t, Bytes(origin, format, []byte(doc)))
}

func TestBytes_ReadsJSONAndYAML(t *testing.T) {
	// A document in memory loads like a file, in every format spelling, and its origin is the one given.
	for _, tc := range []struct {
		format string
		doc    string
	}{
		{"json", `{"name": "api", "log": {"level": "info"}}`},
		{".JSON", `{"name": "api", "log": {"level": "info"}}`},
		{"yaml", "name: api\nlog:\n  level: info\n"},
		{"YML", "name: api\nlog:\n  level: info\n"},
	} {
		c, values, err := loadBytes(t, "admin request", tc.format, tc.doc)

		require.NoError(t, err, tc.format)
		require.Equal(t, "api", c.Name, tc.format)
		require.Equal(t, int64(1), c.Level, tc.format)
		require.Equal(t, "admin request", values["name"].Origin, tc.format)
	}
}

func TestBytes_Problems(t *testing.T) {
	tests := []struct {
		name   string
		format string
		doc    string
		want   []Problem
	}{
		{
			name:   "unsupported format",
			format: "toml",
			doc:    "name = 'api'",
			want: []Problem{{
				Code: CodeUnsupportedFormat, Origin: "req", Value: "toml",
				Message: `format "toml" is not JSON or YAML`, Hint: "use json, yaml or yml",
			}},
		},
		{
			name:   "json syntax error points to the line",
			format: "json",
			doc:    "{\n  \"name\": \n}",
			want: []Problem{{
				Code: CodeParseError, Origin: "req:3:1",
				Message: `jsontext: missing value after object name within "/name" after offset 13`,
			}},
		},
		{
			name:   "a document that isn't an object",
			format: "yaml",
			doc:    "- a\n",
			want:   []Problem{{Code: CodeParseError, Origin: "req", Message: "a document holds an object of settings, not a list"}},
		},
		{
			name:   "unknown key",
			format: "yaml",
			doc:    "nmae: api\n",
			want: []Problem{{
				Code: CodeUnknownKey, Origin: "req", Key: "nmae",
				Message: "nmae is not a known setting", Hint: "did you mean name?",
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadBytes(t, "req", tc.format, tc.doc)
			require.Equal(t, tc.want, requireProblems(t, err))
		})
	}
}

func TestBytes_EmptyDocumentSetsNothing(t *testing.T) {
	c, values, err := loadBytes(t, "req", "yaml", "# nothing yet\n")

	require.NoError(t, err)
	require.Empty(t, values)
	require.Equal(t, testConfig{}, c)
}

func TestError_Scope(t *testing.T) {
	// The scope names what was being loaded, so a mock file's problems don't read as config problems.
	p := Problem{Code: CodeUnknownKey, Origin: "file a.yaml", Key: "x", Message: "x is not a known setting"}

	require.Equal(t, "config: 1 problem\n  file a.yaml: unknown_key: x is not a known setting", (&Error{Problems: []Problem{p}}).Error())
	require.Equal(t, "mock: 1 problem\n  file a.yaml: unknown_key: x is not a known setting", (&Error{Scope: "mock", Problems: []Problem{p}}).Error())
}

func TestSchema_Keys(t *testing.T) {
	// Keys lists settings and the groups they nest under, sorted, as file keys.
	require.Equal(t,
		[]string{"config", "debug", "feature", "feature.mode", "feature.size", "log", "log.level", "log.timeout", "name", "port", "proxy_url", "tags"},
		testSchema().Keys())
}

func TestAny_PassesTheDecodedValue(t *testing.T) {
	// An Any setting gets the decoded value as is, for its Set to decode; env and args can only give text.
	var got []any

	s := NewSchema(Field[struct{}]{
		Key: "data", Kind: Any, Usage: "Data.",
		Set: func(_ *struct{}, v Value) error { got = append(got, v.Any); return nil },
	})

	t.Setenv("ANYTEST_DATA", "from env")

	_, err := s.Load(new(struct{}), []Source{
		Bytes("doc", "yaml", []byte("data: {a: [1, 2.5, x, null, 2024-01-01]}\n")),
		Env("ANYTEST"),
		Args([]string{"--data", "from args"}),
	})

	require.NoError(t, err)
	require.Equal(t, []any{
		map[string]any{"a": []any{int64(1), 2.5, "x", nil, "2024-01-01"}},
		"from env",
		"from args",
	}, got)
}
