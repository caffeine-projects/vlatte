package viewdata

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

var errKVDown = errors.New("kv down")

// downKV is a parameter store that can't answer.
type downKV struct {
	vlatte.KV
}

func (downKV) Has(context.Context, string) (bool, error) { return false, errKVDown }

func TestParameters(t *testing.T) {
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "empty", nil))

	params := NewParameters(t.Context(), kv)

	t.Run("a stored empty value is empty text", func(t *testing.T) {
		v, err := params.Get("empty")
		require.NoError(t, err)
		require.Empty(t, v)
	})

	t.Run("a key that isn't stored is an error, as a missing map key is", func(t *testing.T) {
		_, err := params.Get("nmae")
		require.EqualError(t, err, `parameters: no parameter "nmae"`)

		ok, err := params.Has("nmae")
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("a store that fails fails both", func(t *testing.T) {
		down := NewParameters(t.Context(), downKV{})

		_, err := down.Has("k")
		require.ErrorIs(t, err, errKVDown)

		_, err = down.Get("k")
		require.ErrorIs(t, err, errKVDown)
	})
}
