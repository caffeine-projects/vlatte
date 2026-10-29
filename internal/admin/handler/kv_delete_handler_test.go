package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestDeleteKV_AKeyNotSetIsRemovedAlready(t *testing.T) {
	// A test's cleanup removes its keys whether or not the test set them: a second removal doesn't fail it.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "token", []byte("abc123")))
	base := serve(t, newStore(t), adminapi.Capabilities{KV: kv})

	for range 2 {
		res, body := send(t, request{method: http.MethodDelete, url: base + "/kv/token"})
		require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	}

	has, err := kv.Has(t.Context(), "token")
	require.NoError(t, err)
	require.False(t, has)
}

func TestDeleteKV_AStoreThatFailsAnswersAnError(t *testing.T) {
	// A store that is down fails the request with what it said and the key it was at, rather than answer that the key
	// is gone.
	base := serve(t, newStore(t), adminapi.Capabilities{KV: brokenKV{err: errors.New("redis down")}})

	res, body := send(t, request{method: http.MethodDelete, url: base + "/kv/token"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, `admin: removing key "token": redis down`, doc["detail"])
}
