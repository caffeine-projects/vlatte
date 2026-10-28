package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

// verified returns the verdict of the check of body, a JSON document, at url, an admin API.
func verified(t *testing.T, url, body string) map[string]any {
	t.Helper()

	res, answer := send(t, request{method: "QUERY", url: url + "/requests/verify", body: body})
	require.Equal(t, http.StatusOK, res.StatusCode, answer)

	return conforms(t, res, answer, "RequestVerdict")
}

func TestVerifyRequests(t *testing.T) {
	// A client in any language checks the requests as a Go test does, and gets the verdict: a check that failed is
	// an answer, with why.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "GET", "/b", journal.OutcomeUnmatched),
		entryOf("c", "POST", "/c", journal.OutcomeMatched),
	)
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := verified(t, url, `{"method": "GET", "count": 2}`)
	require.Equal(t, map[string]any{"passed": true, "count": int64(2), "waited_ms": int64(0)}, doc)

	// None counts at least 1.
	doc = verified(t, url, `{"path": ["hasprefix", "/"]}`)
	require.Equal(t, map[string]any{"passed": true, "count": int64(3), "waited_ms": int64(0)}, doc)

	doc = verified(t, url, "")
	require.Equal(t, true, doc["passed"])

	doc = verified(t, url, `{"count": ["lte", 2]}`)
	require.Equal(t, false, doc["passed"])
	require.Equal(t, int64(3), doc["count"])
	require.Len(t, doc["selected"], 3)
	require.NotContains(t, doc, "closest", "the check wanted fewer")

	res, body := send(t, request{method: "QUERY", url: url + "/requests/verify", body: `{"outcome": "unmatched", "count": 0}`, accept: "application/yaml"})
	doc = conforms(t, res, body, "RequestVerdict")
	require.Equal(t, false, doc["passed"])
	require.Equal(t, int64(1), doc["count"])
}

func TestVerifyRequests_AFailureSaysWhatAGoTestSays(t *testing.T) {
	// The verdict of a check is the one a Go test gets for the same journal: one engine checks both.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "POST", "/b", journal.OutcomeUnmatched),
	)
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := verified(t, url, `{"method": "POST", "path": "/a", "count": 1}`)

	want, err := verify.Check(t.Context(), verify.Spec{
		Journal: j, Query: journal.Query{Method: match.Equal("POST"), Path: match.Equal("/a")},
		Count: match.Equal(1), Expect: "exactly 1",
	})
	require.NoError(t, err)
	require.False(t, want.Passed)

	require.Equal(t, want.Report, doc["report"])
	require.Equal(t, map[string]any{
		"seq": int64(2), "id": "b", "line": want.Closest[0].Line,
		"passed": int64(1), "total": int64(2), "missed": []any{"path"},
	}, doc["closest"].([]any)[0])

	// The line of a request leaves out its headers and body: a client gets them by its id.
	require.NotContains(t, doc["report"], "application/json")
	require.NotContains(t, doc["report"], `"id": 7`)
}

func TestVerifyRequests_ChecksAnOrder(t *testing.T) {
	j := journalOf(t,
		entryOf("a", "POST", "/login", journal.OutcomeMatched),
		entryOf("b", "GET", "/health", journal.OutcomeMatched),
		entryOf("c", "POST", "/logout", journal.OutcomeMatched),
	)
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := verified(t, url, `{"in_order": [{"path": "/login"}, {"path": "/logout"}]}`)
	require.Equal(t, map[string]any{"passed": true, "waited_ms": int64(0)}, doc)

	doc = verified(t, url, `{"in_order": [{"path": "/logout"}, {"method": "POST", "path": "/login"}]}`)
	require.Equal(t, false, doc["passed"])
	require.NotContains(t, doc, "count", "an order counts no requests")
	require.Equal(t, "\nExpected requests in this order:\n"+
		"  1. request with path: #3\n"+
		"  2. request with method, path: none after #3, #1 before it\n", doc["report"])

	// After a mark, the login before it is another test's.
	doc = verified(t, url, `{"after": 1, "in_order": [{"path": "/login"}, {"path": "/logout"}]}`)
	require.Equal(t, false, doc["passed"])
}

func TestVerifyRequests_RefusesACheckItCantRun(t *testing.T) {
	// A check that can't run is the client's problem, never a verdict: a check that selected nothing would pass
	// count 0.
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: journalOf(t)})

	for _, body := range []string{
		`{"limit": 1}`,
		`{"count": ["no_such_matcher", 1]}`,
		`{"within": "31s"}`,
		`{"within": 0}`,
		`{"during": "-1s"}`,
		`{"within": "1s", "during": "1s"}`,
		`{"within": "1s", "count": 0}`,
		`{"request": {"path": "/a"}}`,
		`{"in_order": []}`,
		`{"in_order": [{"path": "/a"}], "count": 1}`,
		`{"in_order": [{"path": "/a", "after": 1}]}`,
		`{"in_order": [{"path": "/a", "within": "1s"}]}`,
	} {
		res, answer := send(t, request{method: "QUERY", url: url + "/requests/verify", body: body})
		problem(t, res, answer, http.StatusBadRequest, "invalid_body")
	}
}

func TestVerifyRequests_ACheckOfPartOfTheRequestsIsNoVerdict(t *testing.T) {
	// A journal cleared since the client's mark no longer holds the requests the check counts: count 0 would pass.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})
	require.NoError(t, j.Clear())

	res, body := send(t, request{method: "QUERY", url: url + "/requests/verify", body: `{"after": 0, "count": 0}`})
	doc := problem(t, res, body, http.StatusConflict, "incomplete_journal")
	require.Equal(t, verify.ErrCleared.Error(), doc["detail"])
}

func TestVerifyRequests_AServerWithoutAJournalCantVerify(t *testing.T) {
	url := serve(t, newStore(t))

	res, body := send(t, request{method: "QUERY", url: url + "/requests/verify", body: `{}`})
	problem(t, res, body, http.StatusNotImplemented, "not_supported")
}

func TestVerifyRequests_IsRefusedCrossSite(t *testing.T) {
	// A browser's page of another site must not learn what the server got by the verdicts it reads.
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: journalOf(t)})

	res, body := send(t, request{method: "QUERY", url: url + "/requests/verify", set: func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}})
	problem(t, res, body, http.StatusForbidden, "cross_origin")
}
