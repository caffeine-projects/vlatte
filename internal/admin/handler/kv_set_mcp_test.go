package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestSetKVTool(t *testing.T) {
	// An agent sets the value a template reads, an empty one too.
	kv := vlatte.NewMemoryKV()
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})

	doc := conformsAs(t, call(t, cs, "set_kv", map[string]any{"key": "token", "value": "abc123"}), "json", "KVEntry")
	require.Equal(t, map[string]any{"key": "token", "value": "abc123"}, doc)

	doc = conformsAs(t, call(t, cs, "set_kv", map[string]any{"key": "greeting", "value": ""}), "json", "KVEntry")
	require.Equal(t, map[string]any{"key": "greeting", "value": ""}, doc)

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "abc123", string(got))

	has, err := kv.Has(t.Context(), "greeting")
	require.NoError(t, err)
	require.True(t, has)
}

func TestSetKVTool_AValueIsRequired(t *testing.T) {
	// A call without a value sets nothing, rather than set the key to an empty one.
	kv := vlatte.NewMemoryKV()
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})

	text, failed := callTool(t, cs, "set_kv", map[string]any{"key": "token"})
	require.True(t, failed)
	require.Contains(t, text, `missing properties: ["value"]`)

	has, err := kv.Has(t.Context(), "token")
	require.NoError(t, err)
	require.False(t, has)
}
