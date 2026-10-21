package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestIsPresent(t *testing.T) {
	resYes, _ := Present().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
	resNo, _ := Present().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), ""))
	require.True(t, resYes)
	require.False(t, resNo)

	resYes, _ = Present().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), 1))
	require.True(t, resYes)

	resYes, _ = Present().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), 0))
	require.True(t, resYes)

	p := "test"
	resYes, _ = Present().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), &p))
	resNo, _ = Present().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	require.True(t, resYes)
	require.False(t, resNo)
}
