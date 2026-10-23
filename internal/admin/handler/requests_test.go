package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
)

// journalOf returns a journal of entries, the protocol's own: a path, and its method.
func journalOf(t *testing.T, entries ...*journal.Entry) *journal.Journal {
	t.Helper()

	j, err := journal.New(journal.Options{Dir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })

	for _, e := range entries {
		require.NoError(t, j.Append(e))
	}

	return j
}

func entryOf(id, method, path string, outcome journal.Outcome) *journal.Entry {
	return &journal.Entry{
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
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "GET", "/b", journal.OutcomeUnmatched),
		entryOf("c", "POST", "/c", journal.OutcomeMatched),
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

func TestListRequests_AClientThatLeftGetsNoAnswer(t *testing.T) {
	// A client that leaves stops its query, and the handler answers nothing: no one reads it, and the client's leaving
	// is no internal error of the server.
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	rec := httptest.NewRecorder()
	handler.ListRequests(j).ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/requests", nil))

	require.Empty(t, rec.Header(), "no answer")
	require.Empty(t, rec.Body.String(), "no answer")
}

func TestQueryRequests(t *testing.T) {
	// QUERY selects requests by the keys of a query, with the matchers mock files write, and counts them: limit 0
	// answers the count alone, which is what a client verifying a request needs.
	a := entryOf("a", "GET", "/users/1", journal.OutcomeMatched)
	a.MockID = "get-user"
	c := entryOf("c", "POST", "/users", journal.OutcomeMatched)
	c.MockID = "add-user"
	j := journalOf(t, a, entryOf("b", "GET", "/orders", journal.OutcomeUnmatched), c)
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
		{name: "mock", body: `{"mock_id": "add-user"}`, want: []string{"c"}, count: 1},
		{name: "target in full", body: `{"target": "fake:///orders"}`, want: []string{"b"}, count: 1},
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

func TestQueryRequests_PagesWithTheNextOfAPage(t *testing.T) {
	// A query pages as GET does: the next of a page is the before of the page after it, of the same selection, and
	// the count stays as the pages go.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "GET", "/b", journal.OutcomeMatched),
		entryOf("c", "POST", "/c", journal.OutcomeMatched),
	)
	url := serve(t, newStore(t), admin.Journal(j, nil))

	res, body := send(t, request{method: "QUERY", url: url + "/requests", body: `{"limit": 1, "method": "GET"}`})
	doc := conforms(t, res, body, "RequestList")
	require.Equal(t, []string{"b"}, requestIDs(doc))
	require.Equal(t, int64(2), doc["count"])

	next := fmt.Sprintf(`{"limit": 1, "method": "GET", "before": %d}`, doc["next"])
	res, body = send(t, request{method: "QUERY", url: url + "/requests", body: next})
	doc = conforms(t, res, body, "RequestList")
	require.Equal(t, []string{"a"}, requestIDs(doc))
	require.Equal(t, int64(2), doc["count"])
	require.NotContains(t, doc, "next", "the last page")
}

func TestQueryRequests_OutcomeNamesAreTheWireFormat(t *testing.T) {
	tests := []struct {
		name    string
		outcome journal.Outcome
	}{
		{name: "matched", outcome: journal.OutcomeMatched},
		{name: "unmatched", outcome: journal.OutcomeUnmatched},
		{name: "proxied", outcome: journal.OutcomeProxied},
		{name: "failed", outcome: journal.OutcomeFailed},
		{name: "abandoned", outcome: journal.OutcomeAbandoned},
	}
	j := journalOf(t)
	for _, tt := range tests {
		require.NoError(t, j.Append(entryOf(tt.name, "GET", "/", tt.outcome)))
	}
	url := serve(t, newStore(t), admin.Journal(j, nil))

	for _, format := range []string{"json", "yaml"} {
		for _, tt := range tests {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				// JSON is also YAML: both decoders must accept the documented text names.
				query := fmt.Sprintf(`{"outcome": %q}`, tt.name)
				conformsAs(t, query, format, "RequestQuery")
				res, body := send(t, request{
					method: "QUERY", url: url + "/requests", body: query,
					contentType: "application/" + format, accept: "application/" + format,
				})
				require.Equal(t, http.StatusOK, res.StatusCode, body)
				doc := conforms(t, res, body, "RequestList")
				require.Equal(t, int64(1), doc["count"])
				require.Equal(t, []string{tt.name}, requestIDs(doc))
				entries, _ := doc["requests"].([]any)
				require.Len(t, entries, 1)
				got, _ := entries[0].(map[string]any)
				require.Equal(t, tt.name, got["outcome"])
			})
		}
	}
}

func TestQueryRequests_InvalidOutcomesExplainTheNamesToSend(t *testing.T) {
	url := serve(t, newStore(t), admin.Journal(journalOf(t), nil))
	const names = "matched, unmatched, proxied, failed or abandoned"
	tests := []struct {
		value   string
		message string
		hint    string
	}{
		{value: "0", message: "outcome must be one of " + names + ", not a number"},
		{value: "1", message: "outcome must be one of " + names + ", not a number", hint: "write the name: matched"},
		{value: "2", message: "outcome must be one of " + names + ", not a number", hint: "write the name: unmatched"},
		{value: "3", message: "outcome must be one of " + names + ", not a number", hint: "write the name: proxied"},
		{value: "4", message: "outcome must be one of " + names + ", not a number", hint: "write the name: failed"},
		{value: "5", message: "outcome must be one of " + names + ", not a number", hint: "write the name: abandoned"},
		{value: `"1"`, message: "outcome must be one of " + names + `, not "1"`, hint: "write the name: matched"},
		{value: `"won"`, message: "outcome must be one of " + names + `, not "won"`},
	}

	for _, format := range []string{"json", "yaml"} {
		for _, tt := range tests {
			t.Run(format+"/"+tt.value, func(t *testing.T) {
				res, body := send(t, request{
					method: "QUERY", url: url + "/requests", body: `{"outcome": ` + tt.value + `}`,
					contentType: "application/" + format, accept: "application/" + format,
				})
				doc := problem(t, res, body, http.StatusBadRequest, "invalid_body")
				problems, _ := doc["problems"].([]any)
				require.Len(t, problems, 1)
				got, _ := problems[0].(map[string]any)
				require.Equal(t, "outcome", got["key"])
				require.Equal(t, tt.message, got["message"])
				hint, _ := got["hint"].(string)
				require.Equal(t, tt.hint, hint)
			})
		}
	}
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
		{name: "negative before", body: `{"before": -1}`, key: "before"},
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
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched), entryOf("b", "GET", "/b", journal.OutcomeMatched))

	var got config.Value

	pattern := func(v config.Value) (journal.Filter, error) {
		got = v

		if v.Any == "bad" {
			return nil, config.Invalid(v, "a bad request part", "")
		}

		if v.Any == "failing" {
			return func(context.Context, *journal.Entry) (bool, error) {
				return false, fmt.Errorf("%w: a matcher that fails", vlatte.ErrMatching)
			}, nil
		}

		return func(_ context.Context, e *journal.Entry) (bool, error) { return e.Path == "/b", nil }, nil
	}

	url := serve(t, newStore(t), admin.Journal(j, pattern))

	res, body := send(t, request{method: "QUERY", url: url + "/requests", body: `{"request": {"path": "/b"}, "method": "GET"}`})
	require.Equal(t, []string{"b"}, requestIDs(conforms(t, res, body, "RequestList")))
	require.Equal(t, map[string]any{"path": "/b"}, got.Any)
	require.Equal(t, "request", got.Key)

	res, body = send(t, request{method: "QUERY", url: url + "/requests", body: `{"request": "bad"}`})
	problem(t, res, body, http.StatusBadRequest, "invalid_body")

	// A matcher that fails on a request is the query's problem, not the server's.
	res, body = send(t, request{method: "QUERY", url: url + "/requests", body: `{"request": "failing"}`})
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
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
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
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	url := serve(t, newStore(t), admin.Journal(j, nil))

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
	url := serve(t, newStore(t), admin.Journal(j, nil))

	res, body := send(t, request{method: http.MethodDelete, url: url + "/requests"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, journal.ErrClosed.Error(), doc["detail"])
}

func TestJournal_ANilJournalIsNone(t *testing.T) {
	// A server without a journal passes its nil one: its journal routes answer 501, and a reset works.
	url := serve(t, newStore(t), admin.Journal((*journal.Journal)(nil), nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/requests"})
	problem(t, res, body, http.StatusNotImplemented, "not_supported")

	res, body = send(t, request{method: http.MethodPost, url: url + "/reset"})
	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
}
