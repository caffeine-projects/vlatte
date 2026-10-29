package e2e

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/kvtest"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

var _ vlatte.KV = (*redisKV)(nil)

// _kvStores are the stores the suite runs on. start returns a store that holds seed, and how another client writes to
// it: Set for the memory and CSV stores, and the Redis server itself for the Redis one, as another process would.
var _kvStores = []struct {
	name  string
	start func(t *testing.T, seed map[string]string) (kv vlatte.KV, write func(k, v string))
}{
	{"memory", func(t *testing.T, seed map[string]string) (vlatte.KV, func(k, v string)) {
		kv := vlatte.NewMemoryKV()
		write := func(k, v string) { require.NoError(t, kv.Set(t.Context(), k, []byte(v))) }

		for k, v := range seed {
			write(k, v)
		}

		return kv, write
	}},
	{"csv", func(t *testing.T, seed map[string]string) (vlatte.KV, func(k, v string)) {
		kv := vlatte.MustNewCSVKV(writeCSV(t, seed))
		return kv, func(k, v string) { require.NoError(t, kv.Set(t.Context(), k, []byte(v))) }
	}},
	{"redis", func(t *testing.T, seed map[string]string) (vlatte.KV, func(k, v string)) {
		mr, kv := startRedis(t)
		write := func(k, v string) { require.NoError(t, mr.Set(k, v)) }

		for k, v := range seed {
			write(k, v)
		}

		return kv, write
	}},
}

func TestKV_RedisStoreMeetsTheContract(t *testing.T) {
	// The tests below rest on a Redis store that behaves as the memory one does.
	kvtest.Run(t, func(t *testing.T) vlatte.KV {
		_, kv := startRedis(t)
		return kv
	})
}

func TestKV_EveryTemplateSiteReadsTheStore(t *testing.T) {
	// Replies, body files, multipart parts and webhooks all read the store, from Go and from mock files.
	for _, store := range _kvStores {
		t.Run(store.name, func(t *testing.T) {
			kv, _ := store.start(t, map[string]string{"method": "PUT"})

			checkTemplateSites(t, vlatte.MustNewGoTemplateEngine(),
				`{{ .KV.Get "method" }}-{{ index .Request.URLPathSegments 1 }}`, "PUT-42", mockhttp.Setup().KV(kv))
		})
	}
}

func TestKV_EachRequestReadsTheValueInForce(t *testing.T) {
	// A store another client writes is read on every request, never once when the mock is built, and a key a template
	// checks for with .KV.Has renders once it is set.
	for _, store := range _kvStores {
		t.Run(store.name, func(t *testing.T) {
			kv, write := store.start(t, nil)
			m := startKV(t, kv)

			for _, v := range []string{"v1", "v2"} {
				write("greeting", v)

				for _, path := range []string{"/greeting", "/guarded"} {
					res, body := getTarget(t, m, path)
					require.Equal(t, http.StatusOK, res.StatusCode, string(body))
					require.Equal(t, v, string(body), path)
				}
			}
		})
	}
}

func TestKV_AMissingKeyFailsTheRequest(t *testing.T) {
	// A typo fails loudly on every store, and a key a template checks for with .KV.Has may be missing.
	for _, store := range _kvStores {
		t.Run(store.name, func(t *testing.T) {
			kv, _ := store.start(t, nil)
			m := startKV(t, kv)

			res, body := getTarget(t, m, "/greeting")
			require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
			require.Equal(t, vlatte.FailureReply, res.Header.Get(mockhttp.HeaderVlatteError))
			require.Contains(t, string(body), `key "greeting" is not set: set it, or check for it with .KV.Has`)

			res, body = getTarget(t, m, "/guarded")
			require.Equal(t, http.StatusOK, res.StatusCode, string(body))
			require.Equal(t, "hello", string(body))
		})
	}
}

func TestKV_AStoreThatFailsFailsTheRequest(t *testing.T) {
	// A store that is down fails the request, and the answer names the key it was reading.
	mr, kv := startRedis(t)
	m := startKV(t, kv)

	mr.SetError("ERR redis is down")

	res, body := getTarget(t, m, "/greeting")
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureReply, res.Header.Get(mockhttp.HeaderVlatteError))
	require.Contains(t, string(body), `reading key "greeting": ERR redis is down`)
}

func TestKV_RequestsReadWhileATestWrites(t *testing.T) {
	// A test writes values while the server serves requests that read them: each request reads a value written.
	for _, store := range _kvStores {
		t.Run(store.name, func(t *testing.T) {
			kv, write := store.start(t, map[string]string{"greeting": "v0"})
			m := startKV(t, kv)

			const values = 10

			written := make([]string, values)
			for i := range values {
				written[i] = "v" + strconv.Itoa(i)
			}

			var wg sync.WaitGroup

			for range 8 {
				wg.Go(func() {
					for range values {
						res, err := http.Get(m.URL("/greeting"))
						if !assert.NoError(t, err) {
							return
						}

						b, err := io.ReadAll(res.Body)
						assert.NoError(t, err)
						assert.NoError(t, res.Body.Close())
						assert.Equal(t, http.StatusOK, res.StatusCode, string(b))
						assert.Contains(t, written, string(b))
					}
				})
			}

			for _, v := range written[1:] {
				write("greeting", v)
			}

			wg.Wait()
		})
	}
}

// startKV starts a server that reads kv, with mocks that read the key greeting: GET /greeting reads it, and
// GET /guarded checks for it first, with hello in its place.
func startKV(t *testing.T, kv vlatte.KV) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(mockhttp.Setup().Silent().KV(kv))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/greeting").Reply(mockhttp.OK().BodyTemplate(`{{ .KV.Get "greeting" }}`)))
	m.MustMock(mockhttp.Getf("/guarded").Reply(mockhttp.OK().
		BodyTemplate(`{{ if .KV.Has "greeting" }}{{ .KV.Get "greeting" }}{{ else }}hello{{ end }}`)))

	return m
}

// redisKV is a [vlatte.KV] on Redis, as a user plugs one in.
type redisKV struct {
	c *redis.Client
}

func (kv redisKV) Get(ctx context.Context, k string) ([]byte, error) {
	b, err := kv.c.Get(ctx, k).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: %q", vlatte.ErrKVKeyNotFound, k)
	}

	return b, err
}

func (kv redisKV) Has(ctx context.Context, k string) (bool, error) {
	n, err := kv.c.Exists(ctx, k).Result()
	return n == 1, err
}

func (kv redisKV) Set(ctx context.Context, k string, v []byte) error {
	return kv.c.Set(ctx, k, v, 0).Err()
}

func (kv redisKV) Del(ctx context.Context, k string) error {
	return kv.c.Del(ctx, k).Err()
}

func (kv redisKV) Keys(ctx context.Context) ([]string, error) {
	var keys []string

	it := kv.c.Scan(ctx, 0, "*", 0).Iterator()
	for it.Next(ctx) {
		keys = append(keys, it.Val())
	}

	return keys, it.Err()
}

// startRedis starts a Redis server, and returns it and a store on it. Both stop when t ends.
func startRedis(t *testing.T) (*miniredis.Miniredis, redisKV) {
	t.Helper()

	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })

	return mr, redisKV{c}
}

// writeCSV writes seed as the CSV file of a store, a header then a row for each key, and returns its path.
func writeCSV(t *testing.T, seed map[string]string) string {
	t.Helper()

	var b bytes.Buffer

	w := csv.NewWriter(&b)
	require.NoError(t, w.Write([]string{"key", "value"}))

	for k, v := range seed {
		require.NoError(t, w.Write([]string{k, v}))
	}

	w.Flush()
	require.NoError(t, w.Error())

	path := filepath.Join(t.TempDir(), "kv.csv")
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0o600))

	return path
}
