package handler_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// journalOf returns a journal of entries, the protocol's own: a path, and its method.
func journalOf(t *testing.T, entries ...*vlatte.JournalEntry) *vlatte.Journal {
	t.Helper()

	j, err := vlatte.NewJournal(vlatte.JournalOptions{Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })

	for _, e := range entries {
		require.NoError(t, j.Append(e))
	}

	return j
}

func entryOf(id, method, path string, outcome vlatte.Outcome) *vlatte.JournalEntry {
	return &vlatte.JournalEntry{
		ID:      id,
		Time:    time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		Kind:    "fake",
		Outcome: outcome,
		Method:  method,
		Path:    path,
		Target:  "fake://" + path,
		Status:  200,
		Header:  map[string][]string{"Accept": {"application/json"}},
		Body:    []byte(`{"id": 7}`),
	}
}

// requestIDs returns the ids of the requests of a RequestList.
func requestIDs(doc map[string]any) []string {
	list, _ := doc["requests"].([]any)

	ids := make([]string, 0, len(list))
	for _, r := range list {
		e, _ := r.(map[string]any)
		id, _ := e["id"].(string)
		ids = append(ids, id)
	}

	return ids
}

func TestListRequests(t *testing.T) {
	// GET lists the newest requests a page at a time, in a browser or behind any proxy, with how many there are: the
	// count stays as the pages go.
	j := journalOf(t,
		entryOf("a", "GET", "/a", vlatte.OutcomeMatched),
		entryOf("b", "GET", "/b", vlatte.OutcomeUnmatched),
		entryOf("c", "POST", "/c", vlatte.OutcomeMatched),
	)
	url := serve(t, newStore(t), admin.Journal(j, nil))

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

func TestQueryRequests(t *testing.T) {
	// QUERY selects requests by the keys of a query, with the matchers mock files write, and counts them: limit 0
	// answers the count alone, which is what a client verifying a request needs.
	j := journalOf(t,
		entryOf("a", "GET", "/users/1", vlatte.OutcomeMatched),
		entryOf("b", "GET", "/orders", vlatte.OutcomeUnmatched),
		entryOf("c", "POST", "/users", vlatte.OutcomeMatched),
	)
	url := serve(t, newStore(t), admin.Journal(j, nil))

	tests := []struct {
		name  string
		body  string
		want  []string
		count int64
	}{
		{name: "empty", body: "", want: []string{"c", "b", "a"}, count: 3},
		{name: "glob", body: `{"path": ["glob", "/users*"]}`, want: []string{"c", "a"}, count: 2},
		{name: "text", body: `{"method": "POST"}`, want: []string{"c"}, count: 1},
		{name: "outcome", body: `{"outcome": "unmatched"}`, want: []string{"b"}, count: 1},
		{name: "status as a number", body: `{"status": 200, "kind": "fake"}`, want: []string{"c", "b", "a"}, count: 3},
		{name: "header in any case", body: `{"header": {"accept": ["contains", "json"]}}`, want: []string{"c", "b", "a"}, count: 3},
		{name: "json body", body: `{"body": ["jsonpath", "$.id", ["equal", 7]], "path": "/orders"}`, want: []string{"b"}, count: 1},
		{name: "time", body: `{"since": "2026-10-21T12:00:00Z", "until": "2026-10-21T13:00:00Z", "method": "GET"}`, want: []string{"b", "a"}, count: 2},
		{name: "page", body: `{"limit": 1}`, want: []string{"c"}, count: 3},
		{name: "count only", body: `{"limit": 0, "method": "GET"}`, want: []string{}, count: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := send(t, request{method: "QUERY", url: url + "/requests", body: tt.body})
			require.Equal(t, http.StatusOK, res.StatusCode, body)

			doc := conforms(t, res, body, "RequestList")
			require.Equal(t, tt.want, requestIDs(doc))
			require.Equal(t, tt.count, doc["count"])
		})
	}

	res, body := send(t, request{method: "QUERY", url: url + "/requests", contentType: "application/yaml", body: "path: /orders\n"})
	require.Equal(t, []string{"b"}, requestIDs(conforms(t, res, body, "RequestList")), "YAML too")
}

func TestQueryRequests_ProblemsOfTheQuery(t *testing.T) {
	url := serve(t, newStore(t), admin.Journal(journalOf(t), nil))

	tests := []struct {
		name string
		body string
		key  string
	}{
		{name: "unknown outcome", body: `{"outcome": "won"}`, key: "outcome"},
		{name: "unknown matcher", body: `{"path": ["nope", 1]}`, key: "path"},
		{name: "object as matcher", body: `{"method": {}}`, key: "method"},
		{name: "bad time", body: `{"since": "yesterday"}`, key: "since"},
		{name: "limit over the most", body: `{"limit": 1001}`, key: "limit"},
		{name: "unknown key", body: `{"paths": "/a"}`, key: "paths"},
		{name: "request part of no protocol", body: `{"request": {"method": "GET"}}`, key: "request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, body := send(t, request{method: "QUERY", url: url + "/requests", body: tt.body})

			doc := problem(t, res, body, http.StatusBadRequest, "invalid_body")
			problems, _ := doc["problems"].([]any)
			require.Len(t, problems, 1, body)
			require.Equal(t, tt.key, problems[0].(map[string]any)["key"], body)
		})
	}
}

func TestQueryRequests_TheRequestPartIsTheProtocolsPattern(t *testing.T) {
	// The server's protocol makes a pattern of the request part, as its mocks match: it narrows what the other keys
	// select, and its problems are the query's.
	j := journalOf(t, entryOf("a", "GET", "/a", vlatte.OutcomeMatched), entryOf("b", "GET", "/b", vlatte.OutcomeMatched))

	var got config.Value

	pattern := func(v config.Value) (vlatte.EntryFilter, error) {
		got = v

		if v.Any == "bad" {
			return nil, config.Invalid(v, "a bad request part", "")
		}

		return func(_ context.Context, e *vlatte.JournalEntry) (bool, error) { return e.Path == "/b", nil }, nil
	}

	url := serve(t, newStore(t), admin.Journal(j, pattern))

	res, body := send(t, request{method: "QUERY", url: url + "/requests", body: `{"request": {"path": "/b"}, "method": "GET"}`})
	require.Equal(t, []string{"b"}, requestIDs(conforms(t, res, body, "RequestList")))
	require.Equal(t, map[string]any{"path": "/b"}, got.Any)
	require.Equal(t, "request", got.Key)

	res, body = send(t, request{method: "QUERY", url: url + "/requests", body: `{"request": "bad"}`})
	problem(t, res, body, http.StatusBadRequest, "invalid_body")
}

func TestQueryRequests_IsRefusedCrossSite(t *testing.T) {
	// QUERY is safe, but a browser's page of another site must not read the requests the server got.
	url := serve(t, newStore(t), admin.Journal(journalOf(t), nil))

	r, err := http.NewRequestWithContext(t.Context(), "QUERY", url+"/requests", nil)
	require.NoError(t, err)
	r.Header.Set("Sec-Fetch-Site", "cross-site")

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	problem(t, res, readBody(t, res), http.StatusForbidden, "cross_origin")
}

func TestGetRequest(t *testing.T) {
	j := journalOf(t, entryOf("a", "GET", "/a", vlatte.OutcomeMatched))
	url := serve(t, newStore(t), admin.Journal(j, nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests/a"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	doc := conforms(t, res, body, "JournalEntry")
	require.Equal(t, "a", doc["id"])
	require.Equal(t, `{"id": 7}`, doc["body"])

	res, body = send(t, request{method: http.MethodGet, url: url + "/requests/nope"})
	problem(t, res, body, http.StatusNotFound, "request_not_found")
}

func TestClearRequests(t *testing.T) {
	// A test clears the journal between its cases, on its own or with the rest of the server through POST /reset.
	j := journalOf(t, entryOf("a", "GET", "/a", vlatte.OutcomeMatched))
	url := serve(t, newStore(t), admin.Journal(j, nil))

	res, body := send(t, request{method: http.MethodDelete, url: url + "/requests"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	count, err := j.Count(t.Context(), vlatte.JournalQuery{})
	require.NoError(t, err)
	require.Zero(t, count)

	require.NoError(t, j.Append(entryOf("b", "GET", "/b", vlatte.OutcomeMatched)))

	res, body = send(t, request{method: http.MethodPost, url: url + "/reset"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	count, err = j.Count(t.Context(), vlatte.JournalQuery{})
	require.NoError(t, err)
	require.Zero(t, count, "reset clears the journal")
}

func TestJournal_ANilJournalIsNone(t *testing.T) {
	// A server without a journal passes its nil one: its journal routes answer 501, and a reset works.
	url := serve(t, newStore(t), admin.Journal((*vlatte.Journal)(nil), nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests"})
	problem(t, res, body, http.StatusNotImplemented, "not_supported")

	res, body = send(t, request{method: http.MethodPost, url: url + "/reset"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
}
