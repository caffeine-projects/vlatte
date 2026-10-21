package match

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestAnyOf(t *testing.T) {
	t.Run("should return true if any of the given matchers returns true", func(t *testing.T) {
		ok, err := Any(
			StrictEqual("test"),
			EqualIgnoreCase("dev"),
			ToLower(StrictEqual("TEST")),
			Contain("qa")).
			Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
		require.Nil(t, err)
		require.True(t, ok)
	})

	t.Run("should return false if all of the given matchers returns false", func(t *testing.T) {
		ok, err := Any(
			StrictEqual("abc"),
			EqualIgnoreCase("def"),
			ToLower(StrictEqual("TEST")),
			Contain("dev")).
			Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
		require.Nil(t, err)
		require.False(t, ok)
	})

	t.Run("should return error and false when any error occurs", func(t *testing.T) {
		ok, err := Any(
			StrictEqual("dev"),
			ToUpper(StrictEqual("none")),
			Function(func(v any) (bool, error) {
				return false, errors.New("boom")
			}),
			EqualIgnoreCase("qa")).
			Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
		require.Error(t, err)
		require.False(t, ok)
	})
}
