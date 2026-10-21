package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestToUpperCase(t *testing.T) {
	ok, err := ToUpper(StrictEqual("TEST")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "tesT"))

	require.NoError(t, err)
	require.True(t, ok)
}
