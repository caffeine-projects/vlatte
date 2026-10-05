package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestPredicate(t *testing.T) {
	m := Predicate(func(v any) bool {
		return v.(string) == "ok"
	})

	t.Run("pass", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "ok"))
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("no pass", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "nok"))
		require.NoError(t, err)
		require.False(t, ok)
	})
}
