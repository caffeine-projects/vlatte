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
	require.Equal(t, "http://user:xxxxx@proxy:3128", display("http://user:secret@proxy:3128"))
	require.Equal(t, "http://user@proxy:3128", display("http://user@proxy:3128"))
	require.Equal(t, "not a url ::", display("not a url ::"))
}
