package filter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPredicate(t *testing.T) {
	m := Predicate(func(v any) bool {
		return v.(string) == "ok"
	})

	t.Run("pass", func(t *testing.T) {
		ok, err := m.Match("ok")
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("no pass", func(t *testing.T) {
		ok, err := m.Match("nok")
		require.NoError(t, err)
		require.False(t, ok)
	})
}
