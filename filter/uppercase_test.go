package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestToUpperCase(t *testing.T) {
	ok, err := ToUpper(StrictEqual("TEST")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "tesT"))

	require.NoError(t, err)
	require.True(t, ok)
}
