package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestAnything(t *testing.T) {
	ok, err := Anything().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), ""))
	require.NoError(t, err)
	require.True(t, ok)
}
