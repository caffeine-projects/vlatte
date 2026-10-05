package config

import (
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestArgs_ReadsFlagsDerivedFromKeys(t *testing.T) {
	c, values, err := loadFile(t, Args([]string{
		"-n", "api",
		"--debug",
		"--log-level", "info",
		"-log-timeout=1.5s",
		"--tags", "a", "--tags=b,c",
		"--feature-size", "3",
	}))

	require.NoError(t, err)
	require.Equal(t, testConfig{
		Name:    "api",
		Debug:   true,
		Level:   1,
		Timeout: 1500 * time.Millisecond,
		Tags:    []string{"a", "b", "c"},
		Feature: &testFeature{Size: 3, Mode: "fast"},
	}, c)
	require.Equal(t, "flag -n", values["name"].Origin)
	require.Equal(t, "flag --tags", values["tags"].Origin)
}

func TestArgs_BoolForms(t *testing.T) {
	for args, want := range map[string]bool{"--debug": true, "--debug=true": true, "--debug=false": false} {
		c, _, err := loadFile(t, Args([]string{args}))

		require.NoError(t, err, args)
		require.Equal(t, want, c.Debug, args)
	}
}

func TestArgs_Help(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		_, _, err := loadFile(t, Args([]string{arg}))
		require.ErrorIs(t, err, flag.ErrHelp, arg)
	}
}

func TestArgs_Problems(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []Problem
	}{
		{
			name: "unknown flag",
			args: []string{"--nmae", "api"},
			want: []Problem{{Code: CodeUnknownKey, Origin: "flag --nmae", Message: "--nmae is not a known flag", Hint: "did you mean --name?"}},
		},
		{
			name: "missing value",
			args: []string{"--name"},
			want: []Problem{{Code: CodeMissingValue, Origin: "flag --name", Key: "name", Message: "--name needs a value", Hint: "write --name=<value>"}},
		},
		{
			name: "bool value after a space",
			args: []string{"--debug", "false"},
			want: []Problem{{
				Code:    CodeUnexpectedArgument,
				Origin:  "args",
				Value:   "false",
				Message: `unexpected argument "false": only flags are accepted`,
				Hint:    "a bool flag takes its value after =, as in --debug=false",
			}},
		},
		{
			name: "positional arguments",
			args: []string{"--name", "api", "serve", "--debug"},
			want: []Problem{
				{Code: CodeUnexpectedArgument, Origin: "args", Value: "serve", Message: `unexpected argument "serve": only flags are accepted`},
				{Code: CodeUnexpectedArgument, Origin: "args", Value: "--debug", Message: `unexpected argument "--debug": only flags are accepted`},
			},
		},
		{
			name: "after the terminator",
			args: []string{"--", "--debug"},
			want: []Problem{{Code: CodeUnexpectedArgument, Origin: "args", Value: "--debug", Message: `unexpected argument "--debug": only flags are accepted`}},
		},
		{
			name: "a URL meant for a bool flag keeps its password hidden",
			args: []string{"--debug", "http://user:secret@proxy"},
			want: []Problem{{
				Code:    CodeUnexpectedArgument,
				Origin:  "args",
				Value:   "http://user:xxxxx@proxy",
				Message: `unexpected argument "http://user:xxxxx@proxy": only flags are accepted`,
			}},
		},
		{
			name: "bad flag syntax",
			args: []string{"---name", "api"},
			want: []Problem{{Code: CodeParseError, Origin: "args", Message: "bad flag syntax: ---name"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadFile(t, Args(tc.args))
			require.Equal(t, tc.want, requireProblems(t, err))
		})
	}
}

func TestArgs_NoArgumentsSetNothing(t *testing.T) {
	c, values, err := loadFile(t, Args(nil))

	require.NoError(t, err)
	require.Empty(t, values)
	require.Equal(t, testConfig{}, c)
}
