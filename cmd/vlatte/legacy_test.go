package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLegacyWarnings(t *testing.T) {
	environ := []string{"MOAI_ADDR=:3000", "DZ_DOCKER_HOST=localhost", "VLATTE_ADDR=:4000", "PATH=/bin"}
	exists := func(path string) bool { return path == filepath.Join("testdata", ".moairc.yaml") }

	warnings := legacyWarnings(environ, []string{".", "testdata"}, exists)

	// Settings that used the old names are ignored now: users must be told what to rename.
	require.ElementsMatch(t, []string{
		"environment variable MOAI_ADDR is no longer read: use VLATTE_ADDR",
		"environment variable DZ_DOCKER_HOST is no longer read: use VLATTE_DOCKER_HOST",
		"config file " + filepath.Join("testdata", ".moairc.yaml") + " is no longer read: rename it to " + filepath.Join("testdata", ".vlatterc.yaml"),
	}, warnings)
}

func TestLegacyWarnings_NoLegacySettings(t *testing.T) {
	warnings := legacyWarnings([]string{"VLATTE_ADDR=:4000"}, []string{"."}, func(string) bool { return false })

	require.Empty(t, warnings)
}
