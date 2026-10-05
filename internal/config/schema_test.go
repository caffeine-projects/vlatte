package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testFeature struct {
	Size int64
	Mode string
}

type testConfig struct {
	Name    string
	Port    int64
	Debug   bool
	Level   int64
	Timeout time.Duration
	Tags    []string
	Proxy   string
	Feature *testFeature
}

// feature returns the feature section, turning it on with its defaults.
func feature(c *testConfig) *testFeature {
	if c.Feature == nil {
		c.Feature = &testFeature{Size: 10, Mode: "fast"}
	}

	return c.Feature
}

func testSchema() *Schema[testConfig] {
	return NewSchema(
		Field[testConfig]{Key: "config", Short: "c", Kind: String, Usage: "Config file to load."},
		Field[testConfig]{
			Key: "name", Short: "n", Kind: String, Usage: "Name.",
			Set: func(c *testConfig, v Value) error { c.Name = v.Str; return nil },
		},
		Field[testConfig]{
			Key: "port", Kind: Int, Usage: "Port.", Default: "80",
			Set: func(c *testConfig, v Value) error {
				if v.Int > 65535 {
					return errors.New("must be 65535 or less")
				}

				c.Port = v.Int

				return nil
			},
		},
		Field[testConfig]{
			Key: "debug", Kind: Bool, Usage: "Debug.",
			Set: func(c *testConfig, v Value) error { c.Debug = v.Bool; return nil },
		},
		Field[testConfig]{
			Key: "log.level", Kind: Int, Usage: "Level.", Choices: []Choice{{Name: "debug", Value: 0}, {Name: "info", Value: 1}},
			Set: func(c *testConfig, v Value) error { c.Level = v.Int; return nil },
		},
		Field[testConfig]{
			Key: "log.timeout", Kind: Duration, Usage: "Timeout.",
			Set: func(c *testConfig, v Value) error { c.Timeout = v.Dur; return nil },
		},
		Field[testConfig]{
			Key: "tags", Kind: List, Usage: "Tags.",
			Set: func(c *testConfig, v Value) error { c.Tags = v.List; return nil },
		},
		Field[testConfig]{
			Key: "proxy_url", Kind: String, Usage: "Proxy URL.",
			Set: func(c *testConfig, v Value) error {
				if !strings.HasPrefix(v.Str, "http") {
					return errors.New("must be an http URL")
				}

				c.Proxy = v.Str

				return nil
			},
		},
		Field[testConfig]{
			Key: "feature", Kind: Bool, Usage: "Enable the feature.",
			Set: func(c *testConfig, v Value) error {
				if !v.Bool {
					c.Feature = nil
					return nil
				}

				feature(c)

				return nil
			},
		},
		Field[testConfig]{
			Key: "feature.size", Kind: Int, Usage: "Size.",
			Set: func(c *testConfig, v Value) error { feature(c).Size = v.Int; return nil },
		},
		Field[testConfig]{
			Key: "feature.mode", Kind: String, Usage: "Mode.",
			Set: func(c *testConfig, v Value) error { feature(c).Mode = v.Str; return nil },
		},
	)
}

func TestNewSchema_DerivesNamesFromKeys(t *testing.T) {
	// Users only learn the key: the env var and the flag must follow from it the same way for every key.
	s := testSchema()

	tests := []struct {
		key  string
		flag string
		env  string
	}{
		{"name", "name", "NAME"},
		{"log.level", "log-level", "LOG_LEVEL"},
		{"proxy_url", "proxy-url", "PROXY_URL"},
		{"feature.size", "feature-size", "FEATURE_SIZE"},
	}

	for _, tc := range tests {
		f := s.byKey[tc.key]
		require.Equal(t, tc.flag, f.flag, tc.key)
		require.Equal(t, tc.env, f.env, tc.key)
		require.Same(t, f, s.byFlag[tc.flag], tc.key)
	}

	require.Same(t, s.byKey["name"], s.byFlag["n"], "shorthands resolve to their key")
	require.True(t, s.byKey["feature"].section, "a bool key with nested keys is a section")
	require.False(t, s.byKey["debug"].section)
	require.Contains(t, s.groups, "log", "a prefix without a key of its own is a group")
}

func TestNewSchema_PanicsOnBadDeclarations(t *testing.T) {
	// Declarations are code: a mistake must fail the first test that builds the schema, not a user at runtime.
	set := func(*testConfig, Value) error { return nil }

	tests := []struct {
		name   string
		fields []Field[testConfig]
		panic  string
	}{
		{"uppercase key", []Field[testConfig]{{Key: "Name"}}, `invalid key "Name"`},
		{"empty segment", []Field[testConfig]{{Key: "log..level"}}, `invalid key "log..level"`},
		{"duplicate key", []Field[testConfig]{{Key: "a"}, {Key: "a"}}, `key "a" is declared twice`},
		{"flag and env collision", []Field[testConfig]{{Key: "a.b_c"}, {Key: "a_b.c"}}, `both use flag "a-b-c"`},
		{"short collision", []Field[testConfig]{{Key: "a", Short: "x"}, {Key: "b", Short: "x"}}, `both use flag "x"`},
		{"help flag", []Field[testConfig]{{Key: "help"}}, `can't use flag "help"`},
		{"help shorthand", []Field[testConfig]{{Key: "hosts", Short: "h"}}, `can't use flag "h"`},
		{"long shorthand", []Field[testConfig]{{Key: "a", Short: "ab"}}, "must be one letter"},
		{"choices on a string", []Field[testConfig]{{Key: "a", Kind: String, Choices: []Choice{{Name: "x"}}}}, "only Int settings"},
		{"nested under a non-bool", []Field[testConfig]{{Key: "a", Kind: Int, Set: set}, {Key: "a.b"}}, `nests under "a", which is not a bool`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				require.NotNil(t, r)
				require.Contains(t, r, tc.panic)
			}()

			NewSchema(tc.fields...)
		})
	}
}

func TestKind_String(t *testing.T) {
	// Help shows the kind of each value, as in "--name string".
	kinds := map[Kind]string{String: "string", Bool: "bool", Int: "int", Duration: "duration", List: "list", Kind(9): "Kind(9)"}

	for kind, want := range kinds {
		require.Equal(t, want, kind.String())
	}
}

func TestFlags_DescribeEveryKey(t *testing.T) {
	// The CLI registers these for shell completion, so they must cover every key.
	s := testSchema()
	flags := s.Flags()

	require.Len(t, flags, len(s.fields))
	require.Equal(t, Flag{Name: "config", Shorthand: "c", Usage: "Config file to load."}, flags[0])
	require.Equal(t, Flag{Name: "debug", Usage: "Debug.", Bool: true}, flags[3])
	require.Equal(t, Flag{Name: "log-level", Usage: "Level.", Values: []string{"debug", "info"}}, flags[4])
}

func TestUsage_ShowsEveryWayToSetAKey(t *testing.T) {
	// Help is where users and agents discover settings: each needs its flag, env var, file key,
	// allowed values and default.
	s := NewSchema(
		Field[testConfig]{Key: "name", Short: "n", Kind: String, Usage: "Name."},
		Field[testConfig]{Key: "debug", Kind: Bool, Usage: "Debug."},
		Field[testConfig]{Key: "log.level", Kind: Int, Usage: "Level.", Default: "info", Choices: []Choice{{Name: "debug", Value: 0}, {Name: "info", Value: 1}}},
		Field[testConfig]{Key: "log.timeout", Kind: Duration, Usage: "Timeout."},
	)

	want := `  -n, --name string
        Name.
        env APP_NAME, file key name
  --debug
        Debug.
        env APP_DEBUG, file key debug
  --log-level name
        Level. One of: debug (0), info (1). Default: info.
        env APP_LOG_LEVEL, file key log.level
  --log-timeout duration
        Timeout.
        env APP_LOG_TIMEOUT, file key log.timeout
`

	require.Equal(t, want, s.Usage("APP"))
}
