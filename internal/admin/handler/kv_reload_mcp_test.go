package handler_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestReloadKVTool(t *testing.T) {
	file := writeKVFile(t, "key,value\ntoken,abc123\n")
	kv := vlatte.MustNewCSVKV(file)
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})
	require.NoError(t, os.WriteFile(file, []byte("key,value\ntoken,def456\n"), 0o600))

	require.Equal(t, "done", call(t, cs, "reload_kv", nil))

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "def456", string(got))
}

func TestReloadKVTool_ABrokenFileChangesNothing(t *testing.T) {
	file := writeKVFile(t, "key,value\ntoken,abc123\n")
	kv := vlatte.MustNewCSVKV(file)
	cs := connect(t, newStore(t), adminapi.Capabilities{KV: kv})
	require.NoError(t, os.WriteFile(file, []byte("key,value\n,orphan\n"), 0o600))

	doc := callFails(t, cs, "reload_kv", nil, http.StatusInternalServerError, "load_failed")
	require.Equal(t, "kv: "+file+":2: expected a key, not an empty field", doc["detail"])

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "abc123", string(got))
}
