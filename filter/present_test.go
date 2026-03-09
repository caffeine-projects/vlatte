package filter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsPresent(t *testing.T) {
	resYes, _, _ := Present().Match("test")
	resNo, _, _ := Present().Match("")
	require.True(t, resYes)
	require.False(t, resNo)

	resYes, _, _ = Present().Match(1)
	require.True(t, resYes)

	resYes, _, _ = Present().Match(0)
	require.True(t, resYes)

	p := "test"
	resYes, _, _ = Present().Match(&p)
	resNo, _, _ = Present().Match(nil)
	require.True(t, resYes)
	require.False(t, resNo)
}
