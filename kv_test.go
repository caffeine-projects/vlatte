package vlatte

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKV(t *testing.T) {
	params := NewMemoryKV()
	key1 := "k1"
	val1 := "test"
	key2 := "k2"
	val2 := "100"

	require.NoError(t, params.Set(t.Context(), key1, []byte(val1)))
	require.NoError(t, params.Set(t.Context(), key2, []byte(val2)))

	v, err := params.Get(t.Context(), key1)
	require.NoError(t, err)
	require.Equal(t, val1, string(v))

	v, err = params.Get(t.Context(), key2)
	require.NoError(t, err)
	require.Equal(t, val2, string(v))

	v, err = params.Get(t.Context(), "unknown")
	require.NoError(t, err)
	require.Nil(t, v)

	h1, err := params.Has(t.Context(), key1)
	require.NoError(t, err)
	h2, err := params.Has(t.Context(), key2)
	require.NoError(t, err)
	h3, err := params.Has(t.Context(), "unknown")
	require.NoError(t, err)

	require.True(t, h1)
	require.True(t, h2)
	require.False(t, h3)

	require.NoError(t, params.Del(t.Context(), key1))

	h1, err = params.Has(t.Context(), key1)

	require.NoError(t, err)
	require.False(t, h1)
}

func TestKVRace(t *testing.T) {
	params := NewMemoryKV()
	jobs := 10
	wg := sync.WaitGroup{}

	for i := range jobs {
		wg.Add(1)

		key := "key--" + strconv.FormatInt(int64(i), 10)
		value := "value"

		go func(index int) {
			kk := "key--" + strconv.FormatInt(int64(index), 10)
			vv := "value"

			err := params.Set(t.Context(), kk, []byte(vv))

			assert.NoError(t, err)

			v, err := params.Get(t.Context(), kk)

			assert.NoError(t, err)
			assert.Equal(t, vv, string(v))

			_ = params.Del(t.Context(), "k001")
			_ = params.Del(t.Context(), kk)

			wg.Done()
		}(i)

		err := params.Set(t.Context(), key, []byte(value))
		require.NoError(t, err)
	}

	_, _ = params.Get(t.Context(), "key--100")
	_, _ = params.Get(t.Context(), "key--0")

	_ = params.Set(t.Context(), "k1", []byte("v1"))
	_ = params.Set(t.Context(), "k2", []byte("v2"))
	_ = params.Set(t.Context(), "k1", []byte("v2"))

	wg.Wait()
}
