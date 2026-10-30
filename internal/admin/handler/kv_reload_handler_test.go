package handler_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestReloadKV(t *testing.T) {
	// A test edits the store's file and reloads it: the values it set since are gone, and the file's are back.
	file := writeKVFile(t, "key,value\ntoken,abc123\n")
	kv := vlatte.MustNewCSVKV(file)
	require.NoError(t, kv.Set(t.Context(), "city", []byte("rio")))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})
	require.NoError(t, os.WriteFile(file, []byte("key,value\ntoken,def456\n"), 0o600))

	res, body := send(t, request{method: http.MethodPost, url: base + "/kv/reload"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	keys, err := kv.Keys(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"token"}, keys)

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "def456", string(got))
}

func TestReloadKV_ABrokenFileChangesNothing(t *testing.T) {
	// The answer names the line to fix, and templates read the values the store had.
	file := writeKVFile(t, "key,value\ntoken,abc123\n")
	kv := vlatte.MustNewCSVKV(file)
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})
	require.NoError(t, os.WriteFile(file, []byte("key,value\ntoken,def456\ntoken,ghi789\n"), 0o600))

	res, body := send(t, request{method: http.MethodPost, url: base + "/kv/reload"})

	doc := problem(t, res, body, http.StatusInternalServerError, "load_failed")
	require.Equal(t, "kv: "+file+`:3: key "token" repeats line 2: keep one of them`, doc["detail"])

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "abc123", string(got))
}

func TestReloadKV_AStoreWithoutAFileIsNotSupported(t *testing.T) {
	// A store kept in memory alone has nothing to load: a 501, never a 204 that tells a test its values are back.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: vlatte.NewMemoryKV()})

	res, body := send(t, request{method: http.MethodPost, url: base + "/kv/reload"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't load its key-value store from a file", doc["detail"])
}
