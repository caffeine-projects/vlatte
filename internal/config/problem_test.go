package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestError_OneProblem(t *testing.T) {
	err := &Error{Problems: []Problem{{
		Code:    CodeUnknownKey,
		Origin:  "env APP_LOGLEVEL",
		Message: "APP_LOGLEVEL is not a known setting",
		Hint:    "did you mean APP_LOG_LEVEL?",
	}}}

	require.Equal(t, "config: 1 problem\n  env APP_LOGLEVEL: unknown_key: APP_LOGLEVEL is not a known setting; did you mean APP_LOG_LEVEL?", err.Error())
}

func TestSuggest(t *testing.T) {
	// Suggestions are for typos: a close name is suggested, and an unrelated one is not.
	names := []string{"name", "log-level", "log-timeout"}

	require.Equal(t, "name", suggest("nmae", names))
	require.Equal(t, "log-level", suggest("log-levl", names))
	require.Equal(t, "log-level", suggest("loglevel", names))
	require.Empty(t, suggest("completely-different", names))
}

func TestDisplay_HidesURLPasswords(t *testing.T) {
	// Values reach messages because they are invalid: a password must stay hidden even in a URL that doesn't parse.
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"valid URL", "http://user:secret@proxy:3128", "http://user:xxxxx@proxy:3128"},
		{"user without a password", "http://user@proxy:3128", "http://user@proxy:3128"},
		{"not a URL", "not a url ::", "not a url ::"},
		{"invalid escape", "http://user:p%zz@proxy:3128", "http://user:xxxxx@proxy:3128"},
		{"invalid port", "http://user:secret@proxy:port", "http://user:xxxxx@proxy:port"},
		{"no scheme", "user:secret@proxy:3128", "user:xxxxx@proxy:3128"},
		{"unescaped @ in the password", "http://user:p@ss@proxy:3128", "http://user:xxxxx@proxy:3128"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, display(tc.value))
		})
	}
}
