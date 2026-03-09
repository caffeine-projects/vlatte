package filter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFn(t *testing.T) {
	m := Function(func(v any) (bool, error) {
		return v.(string) == "ok", nil
	})

	t.Run("pass", func(t *testing.T) {
		ok, _, err := m.Match("ok")
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("no pass", func(t *testing.T) {
		ok, _, err := m.Match("nok")
		require.NoError(t, err)
		require.False(t, ok)
	})
}

func TestFnError(t *testing.T) {
	ok, _, err := Function(
		func(_ any) (bool, error) {
			return false, errors.New("boom")
		}).
		Match("ok")

	require.Error(t, err)
	require.False(t, ok)
}
