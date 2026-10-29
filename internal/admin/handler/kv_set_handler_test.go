package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestSetKV(t *testing.T) {
	// A client sets the values its test needs, in JSON or YAML, and templates read them: an empty value is a value.
	kv := vlatte.NewMemoryKV()
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	res, body := send(t, request{method: http.MethodPut, url: base + "/kv/token", body: `{"value": "abc123"}`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"key": "token", "value": "abc123"}, conforms(t, res, body, "KVEntry"))

	res, body = send(t, request{method: http.MethodPut, url: base + "/kv/greeting", contentType: "application/yaml", body: `value: ""`})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, map[string]any{"key": "greeting", "value": ""}, conforms(t, res, body, "KVEntry"))

	got, err := kv.Get(t.Context(), "token")
	require.NoError(t, err)
	require.Equal(t, "abc123", string(got))

	has, err := kv.Has(t.Context(), "greeting")
	require.NoError(t, err)
	require.True(t, has, "an empty value is a value")
}

func TestSetKV_AValueIsRequired(t *testing.T) {
	// A body without a value, or with one that isn't text, sets nothing: a template would read a value the test never
	// meant.
	kv := vlatte.NewMemoryKV()
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	for _, tt := range []struct {
		body string
		key  string
		code string
	}{
		{body: ``, key: "value", code: "missing_value"},
		{body: `{}`, key: "value", code: "missing_value"},
		{body: `{"value": 42}`, key: "value", code: "invalid_value"},
		{body: `{"value": "abc", "ttl": 60}`, key: "ttl", code: "unknown_key"},
	} {
		res, answer := send(t, request{method: http.MethodPut, url: base + "/kv/token", body: tt.body})

		doc := problem(t, res, answer, http.StatusBadRequest, "invalid_body")
		require.Equal(t, "the body has 1 problem", doc["detail"], tt.body)

		problems, _ := doc["problems"].([]any)
		require.Len(t, problems, 1, tt.body)
		item, _ := problems[0].(map[string]any)
		require.Equal(t, tt.key, item["key"], tt.body)
		require.Equal(t, tt.code, item["code"], tt.body)
	}

	keys, err := kv.Keys(t.Context())
	require.NoError(t, err)
	require.Empty(t, keys, "nothing set")
}

func TestSetKV_AStoreThatFailsAnswersAnError(t *testing.T) {
	// A store that is down fails the request with what it said and the key it was at, rather than answer a value it
	// didn't set.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: brokenKV{err: errors.New("redis down")}})

	res, body := send(t, request{method: http.MethodPut, url: base + "/kv/token", body: `{"value": "abc"}`})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, `admin: setting key "token": redis down`, doc["detail"])
}
