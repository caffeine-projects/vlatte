package filter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnything(t *testing.T) {
	ok, err := Anything().Match("")
	require.NoError(t, err)
	require.True(t, ok)
}
