package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestToUpperCase(t *testing.T) {
	ok, err := ToUpper(StrictEqual("TEST")).Match(vlatte.NewFilterArg(t.Context(), "tesT"))

	require.NoError(t, err)
	require.True(t, ok)
}
