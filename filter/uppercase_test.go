package filter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToUpperCase(t *testing.T) {
	ok, err := ToUpper(StrictEqual("TEST")).Match("tesT")

	require.NoError(t, err)
	require.True(t, ok)
}
