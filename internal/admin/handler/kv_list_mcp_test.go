package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestListKVTool(t *testing.T) {
	// An agent learns which keys a template can read.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "token", []byte("abc123")))
	require.NoError(t, kv.Set(t.Context(), "greeting", []byte("hi")))
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})

	doc := conformsAs(t, call(t, cs, "list_kv", nil), "json", "KVKeys")
	require.Equal(t, map[string]any{"keys": []any{"greeting", "token"}}, doc)
}
