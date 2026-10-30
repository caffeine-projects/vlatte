package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestGetKV(t *testing.T) {
	// A client reads back the value its templates read, in JSON or YAML.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "token", []byte("abc123")))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	for _, accept := range []string{"", "application/yaml"} {
		res, body := send(t, request{method: http.MethodGet, url: base + "/kv/token", accept: accept})
		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.Equal(t, map[string]any{"key": "token", "value": "abc123"}, conforms(t, res, body, "KVEntry"), accept)
	}
}

func TestGetKV_AKeyNotSetIsNotFound(t *testing.T) {
	// A typo in a key a test reads back answers 404, as a template that reads it fails, never an empty value.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: vlatte.NewMemoryKV()})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv/tokn"})

	doc := problem(t, res, body, http.StatusNotFound, "key_not_found")
	require.Equal(t, `no value is set for the key "tokn"`, doc["detail"])
}

func TestGetKV_AKeyIsOnePathSegment(t *testing.T) {
	// A key with a slash, a space or text beyond ASCII is sent escaped, and is the key the store holds.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "a/b", []byte("1")))
	require.NoError(t, kv.Set(t.Context(), "café x", []byte("2")))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv/a%2Fb"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"key": "a/b", "value": "1"}, conforms(t, res, body, "KVEntry"))

	res, body = send(t, request{method: http.MethodGet, url: base + "/kv/caf%C3%A9%20x"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"key": "café x", "value": "2"}, conforms(t, res, body, "KVEntry"))
}

func TestGetKV_AKeyNamedReloadIsAKey(t *testing.T) {
	// POST /kv/reload reloads, but a read of a key named reload reaches the key, as for any other.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "reload", []byte("yes")))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv/reload"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"key": "reload", "value": "yes"}, conforms(t, res, body, "KVEntry"))
}

func TestGetKV_BytesThatAreNotUTF8AreReplaced(t *testing.T) {
	// A store holds any bytes, but JSON and YAML hold text: the value answers, with what isn't text replaced, rather
	// than fail.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "bin", []byte{'a', 0xff, 'b'}))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv/bin"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"key": "bin", "value": "a�b"}, conforms(t, res, body, "KVEntry"))
}

func TestGetKV_AStoreThatFailsAnswersAnError(t *testing.T) {
	// A store that is down fails the request with what it said and the key it was at, rather than answer that the key
	// isn't set.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: brokenKV{err: errors.New("redis down")}})

	res, body := send(t, request{method: http.MethodGet, url: base + "/kv/token"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, `admin: reading key "token": redis down`, doc["detail"])
}
