package matcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPredicate(t *testing.T) {
	m := Predicate(func(v any) bool {
		return v.(string) == "ok"
	})

	t.Run("pass", func(t *testing.T) {
		res, err := m.Match("ok")
		require.NoError(t, err)
		require.True(t, res.Pass)
	})

	t.Run("no pass", func(t *testing.T) {
		res, err := m.Match("nok")
		require.NoError(t, err)
		require.False(t, res.Pass)
	})
}
