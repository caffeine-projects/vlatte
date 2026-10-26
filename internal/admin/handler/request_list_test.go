package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestOperations_ListRequestsTakesAPageOfALimitItCanAnswer(t *testing.T) {
	// A page holds whole bodies: a caller apart from GET /requests, which checks its parameter, can't ask for more
	// than the most a page holds, nor for a negative count.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil), Journal: j})

	for _, limit := range []int{-1, 1001} {
		_, err := ops.ListRequests(t.Context(), limit, 0)

		doc := handler.Problem(err, "list_requests")
		require.Equal(t, 400, doc.Status)
		require.Equal(t, "invalid_parameter", doc.Code)
	}

	list, err := ops.ListRequests(t.Context(), 1000, 0)
	require.NoError(t, err)
	require.Equal(t, 1, list.Count)
}
