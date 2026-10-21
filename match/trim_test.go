package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestTrim(t *testing.T) {
	ok, err := Trim(StrictEqual("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "  test  "))

	require.NoError(t, err)
	require.True(t, ok)
}
