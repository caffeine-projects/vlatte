package filter

import (
	"errors"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestAllOf(t *testing.T) {
	t.Run("should return true when all matchers evaluates to true", func(t *testing.T) {
		ok, err := All(
			StrictEqual("test"),
			EqualIgnoreCase("TEST"),
			ToUpper(StrictEqual("TEST")),
			Contain("tes")).
			Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("should return false when just one matcher evaluates to false", func(t *testing.T) {
		ok, err := All(
			StrictEqual("test"),
			EqualIgnoreCase("dev"),
			ToUpper(StrictEqual("TEST")),
			Contain("tes")).
			Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("should return false when all matchers evaluates to false", func(t *testing.T) {
		ok, err := All(
			StrictEqual("dev"),
			EqualIgnoreCase("qa"),
			ToUpper(StrictEqual("none")),
			Contain("blah")).
			Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("should return false and an error when any error occurs", func(t *testing.T) {
		ok, err := All(
			StrictEqual("dev"),
			EqualIgnoreCase("qa"),
			ToUpper(StrictEqual("none")),
			Function(func(v any) (bool, error) {
				return false, errors.New("boom")
			})).
			Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
		require.Error(t, err)
		require.False(t, ok)
	})
}
