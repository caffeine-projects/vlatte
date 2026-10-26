package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// layer is a source that returns fixed entries.
type layer []entry

func (l layer) load(*catalog, lookup) ([]entry, []Problem, error) { return l, nil, nil }

// helperT is a test that can mark helpers: a *testing.T or a *rapid.T.
type helperT interface {
	require.TestingT
	Helper()
}

func requireProblems(t helperT, err error) []Problem {
	t.Helper()

	cfgErr, ok := errors.AsType[*Error](err)
	require.True(t, ok, "want *Error, got %v", err)

	return cfgErr.Problems
}

func TestLoad_LaterSourcesOverrideEarlierOnes(t *testing.T) {
	// Precedence is the order of the sources: each value comes from the last source that set its key,
	// and keys no later source sets keep the earlier value.
	var c testConfig
	values, err := testSchema().Load(&c, []Source{
		layer{{key: "name", raw: "from-file", origin: "file a.yaml"}, {key: "port", raw: int64(1), origin: "file a.yaml"}},
		layer{{key: "name", raw: "from-env", text: true, origin: "env APP_NAME"}},
		layer{{key: "name", raw: "from-flag", text: true, origin: "flag --name"}},
	})

	require.NoError(t, err)
	require.Equal(t, "from-flag", c.Name)
	require.Equal(t, int64(1), c.Port)
	require.Equal(t, "flag --name", values["name"].Origin)
	require.Equal(t, "file a.yaml", values["port"].Origin)
}

func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	// Fixing settings one error at a time is slow: one load must list every problem of every source,
	// in a stable order, each with its origin.
	var c testConfig
	_, err := testSchema().Load(&c, []Source{
		layer{{key: "port", raw: "eighty", origin: "file a.yaml"}},
		layer{{key: "port", raw: "70000", text: true, origin: "env APP_PORT"}, {key: "debug", raw: "maybe", text: true, origin: "env APP_DEBUG"}},
	})

	require.Len(t, requireProblems(t, err), 3)
	require.Equal(t, `config: 3 problems
  env APP_DEBUG: invalid_value: debug must be true or false, not "maybe"
  env APP_PORT: invalid_value: port="70000": must be 65535 or less
  file a.yaml: invalid_value: port must be an integer, not a string`, err.Error())
}

func TestLoad_Sections(t *testing.T) {
	t.Run("a key turns its section on with defaults", func(t *testing.T) {
		var c testConfig
		_, err := testSchema().Load(&c, []Source{layer{{key: "feature.size", raw: int64(5), origin: "file a.yaml"}}})

		require.NoError(t, err)
		require.Equal(t, &testFeature{Size: 5, Mode: "fast"}, c.Feature)
	})

	t.Run("a later source turns it off", func(t *testing.T) {
		var c testConfig
		_, err := testSchema().Load(&c, []Source{
			layer{{key: "feature.size", raw: int64(5), origin: "file a.yaml"}},
			layer{{key: "feature", raw: "false", text: true, origin: "flag --feature"}},
		})

		require.NoError(t, err)
		require.Nil(t, c.Feature)
	})

	t.Run("a later key turns it back on", func(t *testing.T) {
		var c testConfig
		_, err := testSchema().Load(&c, []Source{
			layer{{key: "feature", raw: "false", text: true, origin: "env APP_FEATURE"}},
			layer{{key: "feature.mode", raw: "slow", text: true, origin: "flag --feature-mode"}},
		})

		require.NoError(t, err)
		require.Equal(t, &testFeature{Size: 10, Mode: "slow"}, c.Feature)
	})

	t.Run("on keeps what is already set", func(t *testing.T) {
		c := testConfig{Feature: &testFeature{Size: 1, Mode: "code"}}
		_, err := testSchema().Load(&c, []Source{layer{{key: "feature", raw: true, origin: "file a.yaml"}}})

		require.NoError(t, err)
		require.Equal(t, &testFeature{Size: 1, Mode: "code"}, c.Feature)
	})

	t.Run("off and a key in the same source conflict", func(t *testing.T) {
		c := testConfig{Feature: &testFeature{Size: 1, Mode: "code"}}
		_, err := testSchema().Load(&c, []Source{layer{
			{key: "feature", raw: "false", text: true, origin: "flag --feature"},
			{key: "feature.size", raw: "3", text: true, origin: "flag --feature-size"},
		}})

		problems := requireProblems(t, err)
		require.Equal(t, []Problem{{
			Code:    CodeConflict,
			Origin:  "flag --feature",
			Key:     "feature",
			Value:   "false",
			Message: "feature is false, but flag --feature-size sets feature.size in the same source",
			Hint:    "remove one of them",
		}}, problems)
		require.Equal(t, &testFeature{Size: 1, Mode: "code"}, c.Feature, "neither side of a conflict applies")
	})
}

func TestLoad_SetErrorsHidePasswords(t *testing.T) {
	// Proxy URLs can carry credentials; errors end up in logs and terminals.
	var c testConfig
	_, err := testSchema().Load(&c, []Source{layer{{key: "proxy_url", raw: "ftp://user:secret@proxy", text: true, origin: "env APP_PROXY_URL"}}})

	problems := requireProblems(t, err)
	require.Equal(t, `proxy_url="ftp://user:xxxxx@proxy": must be an http URL`, problems[0].Message)
	require.NotContains(t, err.Error(), "secret")
}

func TestLoad_HelpStopsBeforeLowerSources(t *testing.T) {
	// Help must work even when a config file is missing or broken, so it can explain how to fix it.
	var c testConfig
	_, err := testSchema().Load(&c, []Source{
		File("config", "missing.json"),
		Args([]string{"--name", "x", "-h"}),
	})

	require.ErrorIs(t, err, flag.ErrHelp)
}

func TestLoad_KeysWithoutSetAreReturned(t *testing.T) {
	// Keys like a config path have no field: callers read their winning value from the result.
	var c testConfig
	values, err := testSchema().Load(&c, []Source{
		layer{{key: "config", raw: "a.yaml", text: true, origin: "env APP_CONFIG"}},
		layer{{key: "config", raw: "b.yaml", text: true, origin: "flag --config"}},
	})

	require.NoError(t, err)
	require.Equal(t, "b.yaml", values["config"].Str)
	require.Equal(t, "flag --config", values["config"].Origin)
}

func TestLoadOnly_LetsOtherSettingsThrough(t *testing.T) {
	// The vlatte command reads its own settings from the sources the mock server reads too: the server's settings,
	// which the command doesn't know, are no problem of the command's.
	dir := t.TempDir()
	file := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(file, []byte("name: from-file\nother: 1\nlog:\n  other: x\ngroup:\n  key: 2\n"), 0o600))
	t.Setenv("APP_OTHER", "1")
	t.Setenv("APP_PORT", "81")

	var c testConfig
	values, err := testSchema().LoadOnly(&c, []Source{
		File("config", file),
		Env("APP"),
		Args([]string{"--other", "x", "--debug", "-o", "y", "--other-bool", "--tags", "a", "--more=z", "positional", "--log-level=info", "--", "--name=after"}),
	})

	require.NoError(t, err)
	require.Equal(t, "from-file", c.Name, "flags after -- are no flags")
	require.Equal(t, int64(81), c.Port)
	require.True(t, c.Debug)
	require.Equal(t, []string{"a"}, c.Tags)
	require.Equal(t, int64(1), c.Level)
	require.Len(t, values, 5)
}

func TestLoadOnly_StillChecksItsOwnSettings(t *testing.T) {
	var c testConfig
	_, err := testSchema().LoadOnly(&c, []Source{Args([]string{"--other=1", "--port=eighty"})})

	problems := requireProblems(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "port", problems[0].Key)
}

func TestLoadOnly_HelpStopsBeforeLowerSources(t *testing.T) {
	// Help must work whatever the other sources hold, as Load's does.
	for _, arg := range []string{"-h", "--help"} {
		var c testConfig
		_, err := testSchema().LoadOnly(&c, []Source{layer{{key: "port", raw: "eighty", origin: "file a.yaml"}}, Args([]string{"--other", arg})})

		require.ErrorIs(t, err, flag.ErrHelp, arg)
	}
}
