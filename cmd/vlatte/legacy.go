package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

// Names used by earlier versions of the CLI. They are no longer read.
const (
	_legacyConfigFileName = ".moairc"
	_legacyEnvPrefix      = "MOAI_"
	_legacyDockerHostEnv  = "DZ_DOCKER_HOST"
)

// _legacyConfigExts lists the extensions a legacy config file could be loaded with.
var _legacyConfigExts = []string{"json", "toml", "yaml", "yml", "env", "dotenv"}

// legacyWarnings describes settings that still use the old names, so users know what to rename.
func legacyWarnings(environ []string, dirs []string, exists func(path string) bool) []string {
	var warnings []string

	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")

		switch {
		case name == _legacyDockerHostEnv:
			warnings = append(warnings, fmt.Sprintf("environment variable %s is no longer read: use %s", name, _dockerHostEnv))
		case strings.HasPrefix(name, _legacyEnvPrefix):
			renamed := mockhttp.EnvPrefix + "_" + strings.TrimPrefix(name, _legacyEnvPrefix)
			warnings = append(warnings, fmt.Sprintf("environment variable %s is no longer read: use %s", name, renamed))
		}
	}

	for _, dir := range dirs {
		for _, ext := range _legacyConfigExts {
			legacy := filepath.Join(dir, _legacyConfigFileName+"."+ext)
			if exists(legacy) {
				renamed := filepath.Join(dir, mockhttp.DefaultConfigFileName+"."+ext)
				warnings = append(warnings, fmt.Sprintf("config file %s is no longer read: rename it to %s", legacy, renamed))
			}
		}
	}

	return warnings
}
