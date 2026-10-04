package main

import (
	"bytes"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionFrom(t *testing.T) {
	revision := debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef"}
	modified := debug.BuildSetting{Key: "vcs.modified", Value: "true"}

	tests := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{"installed with a version", debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
		{"built from a checkout", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{revision}}, "0123456789ab"},
		{"built with local changes", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{revision, modified}}, "0123456789ab+dirty"},
		{"no version information", debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "devel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, versionFrom(&tc.info))
		})
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := versionCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(&out)

	require.NoError(t, cmd.Execute())
	require.Equal(t, version()+"\n", out.String())
}
