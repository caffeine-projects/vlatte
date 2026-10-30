package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestDeleteKVTool(t *testing.T) {
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "token", []byte("abc123")))
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})

	require.Equal(t, "done", call(t, cs, "delete_kv", map[string]any{"key": "token"}))
	require.Equal(t, "done", call(t, cs, "delete_kv", map[string]any{"key": "token"}), "a key not set is removed already")

	has, err := kv.Has(t.Context(), "token")
	require.NoError(t, err)
	require.False(t, has)
}
