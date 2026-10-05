package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnv_ReadsNamesDerivedFromKeys(t *testing.T) {
	t.Setenv("APP_NAME", "api")
	t.Setenv("APP_LOG_LEVEL", "info")
	t.Setenv("APP_LOG_TIMEOUT", "2s")
	t.Setenv("APP_TAGS", "a, b")
	t.Setenv("APP_FEATURE_SIZE", "3")
	t.Setenv("APP_PORT", "") // empty variables are ignored, as with viper

	c, values, err := loadFile(t, Env("APP"))

	require.NoError(t, err)
	require.Equal(t, testConfig{
		Name:    "api",
		Level:   1,
		Timeout: 2 * time.Second,
		Tags:    []string{"a", "b"},
		Feature: &testFeature{Size: 3, Mode: "fast"},
	}, c)
	require.Equal(t, "env APP_LOG_LEVEL", values["log.level"].Origin)
	require.NotContains(t, values, "port")
}

func TestEnv_UnknownNamesFail(t *testing.T) {
	// A misspelled variable would otherwise be ignored silently.
	t.Setenv("APP_LOGLEVEL", "1")
	t.Setenv("APP_DOCKER_HOST", "0.0.0.0") // read by something else
	t.Setenv("OTHER_NAME", "x")            // another prefix

	_, _, err := loadFile(t, Env("APP", "APP_DOCKER_HOST"))

	require.Equal(t, []Problem{{
		Code:    CodeUnknownKey,
		Origin:  "env APP_LOGLEVEL",
		Message: "APP_LOGLEVEL is not a known setting",
		Hint:    "did you mean APP_LOG_LEVEL?",
	}}, requireProblems(t, err))
}
