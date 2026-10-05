package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestAnything(t *testing.T) {
	ok, err := Anything().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), ""))
	require.NoError(t, err)
	require.True(t, ok)
}
