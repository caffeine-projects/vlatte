package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestListKV(t *testing.T) {
	// A client learns which keys its templates can read, in an order that doesn't change from call to call.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "token", []byte("abc123")))
	require.NoError(t, kv.Set(t.Context(), "greeting", []byte("hi")))
	require.NoError(t, kv.Set(t.Context(), "banner", []byte("")))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"keys": []any{"banner", "greeting", "token"}}, conforms(t, res, body, "KVKeys"))
}

func TestListKV_AnEmptyStoreListsNoKey(t *testing.T) {
	// A client reads the list as an array, empty or not.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: vlatte.NewMemoryKV()})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"keys": []any{}}, conforms(t, res, body, "KVKeys"))
}

func TestListKV_NotSupported(t *testing.T) {
	base := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't keep a key-value store", doc["detail"])
}

func TestListKV_AStoreThatFailsAnswersAnError(t *testing.T) {
	// A store that is down, such as a Redis server that stopped, fails the request with what the store said, rather
	// than list no key.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: brokenKV{err: errors.New("redis down")}})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, "admin: listing keys: redis down", doc["detail"])
}
