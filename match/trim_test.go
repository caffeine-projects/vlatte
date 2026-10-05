package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestTrim(t *testing.T) {
	ok, err := Trim(StrictEqual("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "  test  "))

	require.NoError(t, err)
	require.True(t, ok)
}
