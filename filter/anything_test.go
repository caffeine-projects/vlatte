package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestAnything(t *testing.T) {
	ok, err := Anything().Match(vlatte.NewFilterArg(t.Context(), explain.New(), ""))
	require.NoError(t, err)
	require.True(t, ok)
}
