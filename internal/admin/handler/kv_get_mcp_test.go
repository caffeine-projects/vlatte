package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestGetKVTool(t *testing.T) {
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "token", []byte("abc123")))
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})

	doc := conformsAs(t, call(t, cs, "get_kv", map[string]any{"key": "token"}), "json", "KVEntry")
	require.Equal(t, map[string]any{"key": "token", "value": "abc123"}, doc)

	doc = callFails(t, cs, "get_kv", map[string]any{"key": "tokn"}, http.StatusNotFound, "key_not_found")
	require.Equal(t, `no value is set for the key "tokn"`, doc["detail"])
}
