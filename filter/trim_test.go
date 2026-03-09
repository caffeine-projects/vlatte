package filter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrim(t *testing.T) {
	ok, _, err := Trim(StrictEqual("test")).Match("  test  ")

	require.NoError(t, err)
	require.True(t, ok)
}
