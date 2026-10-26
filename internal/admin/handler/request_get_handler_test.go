package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestGetRequest(t *testing.T) {
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	url := serve(t, newStore(t), admin.Journal(j, nil, nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests/a"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	doc := conforms(t, res, body, "JournalEntry")
	require.Equal(t, "a", doc["id"])
	require.Equal(t, `{"id": 7}`, doc["body"])

	res, body = send(t, request{method: http.MethodGet, url: url + "/requests/nope"})
	problem(t, res, body, http.StatusNotFound, "request_not_found")
}
