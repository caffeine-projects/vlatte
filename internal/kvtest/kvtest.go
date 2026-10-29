// Package kvtest checks that a key-value store keeps the contract of [github.com/caffeine-projects/vlatte.KV]. A store
// runs [Run] from its own tests.
package kvtest

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// Run runs the suite: each test gets an empty store from newKV, which releases it when t ends.
func Run(t *testing.T, newKV func(t *testing.T) vlatte.KV) {
	t.Run("a value set is the value read", func(t *testing.T) {
		kv := newKV(t)
		require.NoError(t, kv.Set(t.Context(), "name", []byte("ana")))

		ok, err := kv.Has(t.Context(), "name")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "ana", get(t, kv, "name"))
	})

	t.Run("a key never set isn't found", func(t *testing.T) {
		kv := newKV(t)

		ok, err := kv.Has(t.Context(), "name")
		require.NoError(t, err)
		require.False(t, ok)

		_, err = kv.Get(t.Context(), "name")
		require.ErrorIs(t, err, vlatte.ErrKVKeyNotFound)
	})

	t.Run("a value set again replaces the first", func(t *testing.T) {
		kv := newKV(t)
		require.NoError(t, kv.Set(t.Context(), "name", []byte("ana")))
		require.NoError(t, kv.Set(t.Context(), "name", []byte("bia")))

		require.Equal(t, "bia", get(t, kv, "name"))
	})

	t.Run("a removed key is gone, and removing it again is not an error", func(t *testing.T) {
		kv := newKV(t)
		require.NoError(t, kv.Set(t.Context(), "name", []byte("ana")))
		require.NoError(t, kv.Set(t.Context(), "city", []byte("rio")))

		require.NoError(t, kv.Del(t.Context(), "name"))
		require.NoError(t, kv.Del(t.Context(), "name"))
		require.NoError(t, kv.Del(t.Context(), "never-set"))

		ok, err := kv.Has(t.Context(), "name")
		require.NoError(t, err)
		require.False(t, ok)

		_, err = kv.Get(t.Context(), "name")
		require.ErrorIs(t, err, vlatte.ErrKVKeyNotFound)
		require.Equal(t, "rio", get(t, kv, "city"), "removing a key keeps the others")
	})

	t.Run("keys lists every key stored, and none removed", func(t *testing.T) {
		kv := newKV(t)

		keys, err := kv.Keys(t.Context())
		require.NoError(t, err)
		require.Empty(t, keys, "an empty store lists none")

		for _, k := range []string{"name", "city", "token"} {
			require.NoError(t, kv.Set(t.Context(), k, []byte("v")))
		}

		require.NoError(t, kv.Del(t.Context(), "city"))

		keys, err = kv.Keys(t.Context())
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"name", "token"}, keys)
	})

	t.Run("an empty value is stored", func(t *testing.T) {
		// A template guards a key with .KV.Has: a key set to nothing is set, so it renders as empty text.
		kv := newKV(t)
		require.NoError(t, kv.Set(t.Context(), "nil", nil))
		require.NoError(t, kv.Set(t.Context(), "empty", []byte{}))

		for _, k := range []string{"nil", "empty"} {
			ok, err := kv.Has(t.Context(), k)
			require.NoError(t, err)
			require.True(t, ok, k)
			require.Equal(t, "", get(t, kv, k), k)
		}
	})

	t.Run("a value is a copy, both ways", func(t *testing.T) {
		// A caller reuses its buffers, and a template engine may write to what it reads: neither changes the store.
		kv := newKV(t)
		in := []byte("ana")
		require.NoError(t, kv.Set(t.Context(), "name", in))
		copy(in, "bia")

		out, err := kv.Get(t.Context(), "name")
		require.NoError(t, err)
		require.Equal(t, "ana", string(out), "a slice given to Set is not the value stored")

		copy(out, "bia")
		require.Equal(t, "ana", get(t, kv, "name"), "a slice Get returns is not the value stored")
	})

	t.Run("any bytes under any key round-trip", func(t *testing.T) {
		// Keys and values come from mock files, the admin API and CSV files: none is limited to plain words.
		kv := newKV(t)
		values := map[string]string{
			"with space":    "a b",
			"tenant:1:name": `"quoted", with a comma`,
			"ключ":          "значение",
			"lines":         "one\ntwo\r\n",
			"binary":        "\x00\xff\xfe",
		}

		for k, v := range values {
			require.NoError(t, kv.Set(t.Context(), k, []byte(v)))
		}

		for k, v := range values {
			require.Equal(t, v, get(t, kv, k), k)
		}
	})

	t.Run("concurrent reads and writes keep each key's last write", func(t *testing.T) {
		// A server serves requests at once, each reading the store, while a test or the admin API writes it.
		kv := newKV(t)
		require.NoError(t, kv.Set(t.Context(), "shared", []byte("v")))

		const (
			workers = 8
			writes  = 20
		)

		var wg sync.WaitGroup

		for w := range workers {
			wg.Go(func() {
				ctx := t.Context()
				key := "worker-" + strconv.Itoa(w)

				for i := range writes {
					want := strconv.Itoa(i)
					assert.NoError(t, kv.Set(ctx, key, []byte(want)))

					v, err := kv.Get(ctx, key)
					assert.NoError(t, err)
					assert.Equal(t, want, string(v), "a worker reads its own last write")

					v, err = kv.Get(ctx, "shared")
					assert.NoError(t, err)
					assert.Equal(t, "v", string(v))

					_, err = kv.Has(ctx, "worker-"+strconv.Itoa((w+1)%workers))
					assert.NoError(t, err)

					keys, err := kv.Keys(ctx)
					assert.NoError(t, err)
					assert.Contains(t, keys, "shared")

					if i%5 == 2 {
						assert.NoError(t, kv.Del(ctx, key))

						ok, err := kv.Has(ctx, key)
						assert.NoError(t, err)
						assert.False(t, ok, "a worker reads its own removal")
					}
				}
			})
		}

		wg.Wait()

		for w := range workers {
			require.Equal(t, strconv.Itoa(writes-1), get(t, kv, "worker-"+strconv.Itoa(w)))
		}
	})
}

// get returns the value stored under k as text, failing t when there is none.
func get(t *testing.T, kv vlatte.KV, k string) string {
	t.Helper()

	v, err := kv.Get(t.Context(), k)
	require.NoError(t, err)

	return string(v)
}
