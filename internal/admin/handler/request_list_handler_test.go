package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestListRequests(t *testing.T) {
	// GET lists the newest requests a page at a time, in a browser or behind any proxy, with how many there are: the
	// count stays as the pages go.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "GET", "/b", journal.OutcomeUnmatched),
		entryOf("c", "POST", "/c", journal.OutcomeMatched),
	)
	url := serve(t, newStore(t), admin.Journal(j, nil, nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests?limit=2"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	doc := conforms(t, res, body, "RequestList")
	require.Equal(t, []string{"c", "b"}, requestIDs(doc))
	require.Equal(t, int64(3), doc["count"])

	res, body = send(t, request{method: http.MethodGet, url: url + "/requests?limit=2&before=" + strconv.FormatInt(doc["next"].(int64), 10)})
	doc = conforms(t, res, body, "RequestList")
	require.Equal(t, []string{"a"}, requestIDs(doc))
	require.Equal(t, int64(3), doc["count"])
	require.NotContains(t, doc, "next", "the last page")

	res, body = send(t, request{method: http.MethodGet, url: url + "/requests", accept: "application/yaml"})
	require.Equal(t, []string{"c", "b", "a"}, requestIDs(conforms(t, res, body, "RequestList")))

	for _, params := range []string{"limit=-1", "limit=1001", "limit=x", "before=-1"} {
		res, body = send(t, request{method: http.MethodGet, url: url + "/requests?" + params})
		problem(t, res, body, http.StatusBadRequest, "invalid_parameter")
	}
}

func TestListRequests_AClientThatLeftGetsNoAnswer(t *testing.T) {
	// A client that leaves stops its query, and the handler answers nothing: no one reads it, and the client's leaving
	// is no internal error of the server.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	rec := httptest.NewRecorder()
	ops := handler.NewOperations(newStore(t), nil, handler.Capabilities{Journal: j})
	handler.ListRequests(ops).ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/requests", nil))

	require.Empty(t, rec.Header(), "no answer")
	require.Empty(t, rec.Body.String(), "no answer")
}

func TestJournal_ANilJournalIsNone(t *testing.T) {
	// A server without a journal passes its nil one: its journal routes answer 501, and a reset works.
	url := serve(t, newStore(t), admin.Journal((*journal.Journal)(nil), nil, nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests"})
	problem(t, res, body, http.StatusNotImplemented, "not_supported")

	res, body = send(t, request{method: http.MethodPost, url: url + "/reset"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
}
