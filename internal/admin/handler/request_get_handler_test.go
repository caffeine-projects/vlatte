package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestGetRequest(t *testing.T) {
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests/a"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	doc := conforms(t, res, body, "JournalEntry")
	require.Equal(t, "a", doc["id"])
	require.Equal(t, `{"id": 7}`, doc["body"])

	res, body = send(t, request{method: http.MethodGet, url: url + "/requests/nope"})
	problem(t, res, body, http.StatusNotFound, "request_not_found")
}

func TestGetRequest_AJournalThatFailsIsTheServersProblem(t *testing.T) {
	// A journal that can't read is no missing request: the client can't fix it, so the answer is a 500 that says why.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	require.NoError(t, j.Close())
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests/a"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, journal.ErrClosed.Error(), doc["detail"])
}
