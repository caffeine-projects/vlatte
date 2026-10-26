package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestClearRequestsTool(t *testing.T) {
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	cs := connect(t, newStore(t), adminapi.Capabilities{Journal: j})

	require.Equal(t, "done", call(t, cs, "clear_requests", nil))

	count, err := j.Count(t.Context(), journal.Query{})
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestClearRequestsTool_AJournalThatFailsToClear(t *testing.T) {
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	require.NoError(t, j.Close())
	cs := connect(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := callFails(t, cs, "clear_requests", nil, http.StatusInternalServerError, "internal")
	require.Equal(t, journal.ErrClosed.Error(), doc["detail"])
}
