package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestListMocksTool(t *testing.T) {
	cs := connect(t, newStore(t, newFake("a"), newFake("b")))

	doc := conformsAs(t, call(t, cs, "list_mocks", nil), "json", "MockList")
	require.Equal(t, []string{"a", "b"}, mockIDs(doc))
}

func TestListMocksTool_AStoreThatFails(t *testing.T) {
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock]), pickAllErr: errors.New("store: down")}
	cs := connect(t, store)

	doc := callFails(t, cs, "list_mocks", nil, http.StatusInternalServerError, "internal")
	require.Equal(t, "store: down", doc["detail"])
}

// mockIDs returns the ids of the mocks of a MockList.
func mockIDs(doc map[string]any) []string {
	list, _ := doc["mocks"].([]any)

	ids := make([]string, 0, len(list))
	for _, m := range list {
		s, _ := m.(map[string]any)
		id, _ := s["id"].(string)
		ids = append(ids, id)
	}

	return ids
}
