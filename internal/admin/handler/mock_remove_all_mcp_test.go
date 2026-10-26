package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestRemoveAllMocksTool(t *testing.T) {
	store := newStore(t, newFake("a"), newFake("b"))
	cs := connect(t, store)

	require.Equal(t, "done", call(t, cs, "remove_all_mocks", nil))
	require.Empty(t, ids(t, store))
}

func TestRemoveAllMocksTool_AStoreThatFails(t *testing.T) {
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), clearErr: errors.New("store: down")}
	cs := connect(t, store)

	doc := callFails(t, cs, "remove_all_mocks", nil, http.StatusInternalServerError, "internal")
	require.Equal(t, "store: down", doc["detail"])
}
