package filter

import (
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestPeek(t *testing.T) {
	t.Run("should return peek error", func(t *testing.T) {
		actionErr := fmt.Errorf("action failed")
		ok, err := Peek(StrictEqual("test"), func(_ any) error { return actionErr }).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))

		require.Error(t, err)
		require.Contains(t, err.Error(), actionErr.Error())
		require.False(t, ok)
	})

	t.Run("should execute action before returning provided matcher expected", func(t *testing.T) {
		c := ""
		ok, err := Peek(StrictEqual("test"), func(v any) error { c = v.(string); return nil }).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))

		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "test", c)
	})
}
