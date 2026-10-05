package mockhttp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestANSI(t *testing.T) {
	on, off := ansi(true), ansi(false)

	require.Equal(t, "\x1b[34mGET\x1b[39m", on.Blue("GET"))
	// Bold closes with its own code, so the color around it still applies after it.
	require.Equal(t, "\x1b[94m\x1b[1mREQUEST\x1b[22m\x1b[39m", on.BrightBlue(on.Bold("REQUEST")))
	require.Equal(t, "GET", off.Blue("GET"))
}

func TestColorsEnabled(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	require.True(t, colorsEnabled(true, true))
	require.False(t, colorsEnabled(false, true), "colors disabled in the config")
	require.False(t, colorsEnabled(true, false), "escape codes would end up as noise in files and pipes")

	t.Setenv("TERM", "dumb")
	require.False(t, colorsEnabled(true, true), "TERM=dumb terminals can't render colors")

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	require.False(t, colorsEnabled(true, true), "NO_COLOR opts out")
}
