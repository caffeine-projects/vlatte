package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestClearRequests(t *testing.T) {
	// A test clears the journal between its cases, on its own or with the rest of the server through POST /reset.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	url := serve(t, newStore(t), admin.Journal(j, nil, nil))

	res, body := send(t, request{method: http.MethodDelete, url: url + "/requests"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	count, err := j.Count(t.Context(), journal.Query{})
	require.NoError(t, err)
	require.Zero(t, count)

	require.NoError(t, j.Append(entryOf("b", "GET", "/b", journal.OutcomeMatched)))

	res, body = send(t, request{method: http.MethodPost, url: url + "/reset"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	count, err = j.Count(t.Context(), journal.Query{})
	require.NoError(t, err)
	require.Zero(t, count, "reset clears the journal")
}

func TestClearRequests_AJournalThatFailsToClear(t *testing.T) {
	// A journal that can't clear says so, and why: a test that went on would verify against the requests of the case
	// before.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	require.NoError(t, j.Close())
	url := serve(t, newStore(t), admin.Journal(j, nil, nil))

	res, body := send(t, request{method: http.MethodDelete, url: url + "/requests"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, journal.ErrClosed.Error(), doc["detail"])
}
