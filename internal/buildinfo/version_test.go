package buildinfo

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionFrom_NamesTheVlatteTheProgramWasBuiltWith(t *testing.T) {
	// The CLI, the admin home page and MCP clients show this version: one answer for one build, and devel when the
	// build carries none, such as go build -buildvcs=false or go run.
	vlatte := func(version string) debug.Module {
		return debug.Module{Path: "github.com/caffeine-projects/vlatte", Version: version}
	}
	other := debug.Module{Path: "example.com/app", Version: "v0.4.0"}

	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"installed with a version", &debug.BuildInfo{Main: vlatte("v1.2.3")}, "v1.2.3"},
		{"built from a checkout", &debug.BuildInfo{Main: vlatte("v0.0.0-20261029224014-2946bcd2b88d+dirty")}, "v0.0.0-20261029224014-2946bcd2b88d+dirty"},
		{"a dependency of the program", &debug.BuildInfo{Main: other, Deps: []*debug.Module{new(vlatte("v1.2.3"))}}, "v1.2.3"},
		{"built without version control", &debug.BuildInfo{Main: vlatte("(devel)")}, "devel"},
		{"not in the build", &debug.BuildInfo{Main: other}, "devel"},
		{"no build information", nil, "devel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, versionFrom(tc.info))
		})
	}
}
