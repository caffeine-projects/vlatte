package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/filterbuilder"
	"github.com/caffeine-projects/vlatte/match"
)

var _ Journal = (*vlatte.Journal)(nil)

const (
	// _requestsPage is how many requests a list answers when it names no limit, and _maxRequestsPage the most it
	// answers: a page holds whole bodies.
	_requestsPage    = 100
	_maxRequestsPage = 1000
)

// Journal is the journal of the requests a mock server serves, such as a *vlatte.Journal.
type Journal interface {
	Find(ctx context.Context, q vlatte.JournalQuery, filters ...vlatte.EntryFilter) (vlatte.JournalPage, error)
	Count(ctx context.Context, q vlatte.JournalQuery, filters ...vlatte.EntryFilter) (int, error)
	Get(id string) (*vlatte.JournalEntry, error)
	Clear() error
}

// RequestPattern makes the filter of the request part of a mock of the server's protocol, v, as a mock file writes
// it: the request key of a query.
type RequestPattern func(v config.Value) (vlatte.EntryFilter, error)

// requestList is the answer of GET and QUERY /requests: count is how many requests the query selects, whatever page
// requests holds, and next, the before of the next page, when there is one.
type requestList struct {
	Count    int                    `json:"count"`
	Requests []*vlatte.JournalEntry `json:"requests"`
	Next     uint64                 `json:"next,omitzero"`
}

// requestQuery is the body of QUERY /requests: a query, the request part of a mock, and a page.
type requestQuery struct {
	q       vlatte.JournalQuery
	request *config.Value
	limit   int
}

var _requestQuerySchema = config.NewSchema(
	config.Field[requestQuery]{
		Key: "kind", Kind: config.String, Usage: "The protocol of the requests, such as http.",
		Set: func(c *requestQuery, v config.Value) error { c.q.Kind = vlatte.Kind(v.Str); return nil },
	},
	config.Field[requestQuery]{
		Key: "outcome", Kind: config.String, Usage: "What came of the requests: matched, unmatched, proxied, failed or abandoned.",
		Set: func(c *requestQuery, v config.Value) error {
			c.q.Outcome = vlatte.Outcome(v.Str)
			if !c.q.Outcome.Valid() {
				return config.Invalid(v, fmt.Sprintf("outcome %q is no outcome", v.Str), "use matched, unmatched, proxied, failed or abandoned")
			}

			return nil
		},
	},
	queryMatcher("mock_id", "The mock that matched", func(c *requestQuery, m vlatte.Matcher) { c.q.MockID = m }),
	queryMatcher("method", "The method", func(c *requestQuery, m vlatte.Matcher) { c.q.Method = m }),
	queryMatcher("path", "The path", func(c *requestQuery, m vlatte.Matcher) { c.q.Path = m }),
	queryMatcher("target", "What the requests asked for in full, such as the URL", func(c *requestQuery, m vlatte.Matcher) { c.q.Target = m }),
	queryMatcher("status", "The status of the answer", func(c *requestQuery, m vlatte.Matcher) { c.q.Status = m }),
	queryMatcher("body", "The body, as text", func(c *requestQuery, m vlatte.Matcher) { c.q.Body = m }),
	config.Field[requestQuery]{
		Key: "header", Kind: config.Any, Usage: "Headers, or metadata, by name, in any case: text or a matcher list each.",
		Set: func(c *requestQuery, v config.Value) error {
			c.q.Header = make(map[string]vlatte.Matcher)

			return config.Members(v, func(name string, member config.Value) error {
				m, err := matcherOf(member)
				c.q.Header[name] = m

				return err
			})
		},
	},
	config.Field[requestQuery]{
		Key: "since", Kind: config.String, Usage: "The earliest time of the requests, in RFC 3339.",
		Set: func(c *requestQuery, v config.Value) error { return parseTime(v, &c.q.Since) },
	},
	config.Field[requestQuery]{
		Key: "until", Kind: config.String, Usage: "The time the requests came before, in RFC 3339.",
		Set: func(c *requestQuery, v config.Value) error { return parseTime(v, &c.q.Until) },
	},
	config.Field[requestQuery]{
		Key: "request", Kind: config.Any, Usage: "The request part of a mock of the server's protocol, as a mock file writes it.",
		Set: func(c *requestQuery, v config.Value) error { c.request = &v; return nil },
	},
	config.Field[requestQuery]{
		Key: "limit", Kind: config.Int, Usage: "The most requests to answer, the newest first. 0 answers the count only.",
		Set: func(c *requestQuery, v config.Value) error {
			c.limit = int(v.Int)
			return checkLimit(v)
		},
	},
	config.Field[requestQuery]{
		Key: "before", Kind: config.Int, Usage: "Answer the requests before this one, the next of a page.",
		Set: func(c *requestQuery, v config.Value) error {
			if v.Int < 0 {
				return config.Invalid(v, "before must be 0 or more", "send the next of a page")
			}

			c.q.Before = uint64(v.Int)

			return nil
		},
	},
)

// ListRequests answers the newest requests the server served: GET /requests. The limit and before parameters page
// through them.
func ListRequests(journal Journal) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		limit, before, err := pageOf(r)
		if err != nil {
			fail(w, r, err)
			return
		}

		answerRequests(w, r, journal, vlatte.JournalQuery{Before: before}, nil, limit)
	})
}

// QueryRequests answers the requests the query of the body selects, and their count: QUERY /requests. A request
// part, which the server's protocol makes a pattern of, narrows them as a mock matches. An empty body selects them
// all.
func QueryRequests(journal Journal, pattern RequestPattern) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		query := requestQuery{limit: _requestsPage}
		if err := decodeBody(_requestQuerySchema, &query, data, format); err != nil {
			fail(w, r, invalid("the query", err))
			return
		}

		var filters []vlatte.EntryFilter

		if query.request != nil {
			filter, err := requestFilter(*query.request, pattern)
			if err != nil {
				fail(w, r, invalid("the query", err))
				return
			}

			filters = append(filters, filter)
		}

		answerRequests(w, r, journal, query.q, filters, query.limit)
	})
}

// GetRequest answers the request of the path's id: GET /requests/{id}.
func GetRequest(journal Journal) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		entry, err := journal.Get(id)
		if errors.Is(err, vlatte.ErrRequestNotFound) {
			err = &problemError{
				status: http.StatusNotFound, code: _codeRequestNotFound,
				detail: fmt.Sprintf("the journal holds no request of the id %q", id),
			}
		}

		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, entry)
	})
}

// ClearRequests removes every request of the journal: DELETE /requests.
func ClearRequests(journal Journal) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		if err := journal.Clear(); err != nil {
			fail(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// answerRequests answers the requests q, with filters, selects, a page of limit of them, or their count only for 0.
// A matcher of the query that panics is the query's problem.
func answerRequests(w http.ResponseWriter, r *http.Request, journal Journal, q vlatte.JournalQuery, filters []vlatte.EntryFilter, limit int) {
	var (
		list requestList
		err  error
	)

	if limit == 0 {
		list.Count, err = journal.Count(r.Context(), q, filters...)
	} else {
		q.Limit = limit

		var page vlatte.JournalPage
		if page, err = journal.Find(r.Context(), q, filters...); err == nil {
			list = requestList{Count: page.Count, Requests: page.Entries, Next: page.Next}
		}
	}

	switch {
	case err == nil:
	case r.Context().Err() != nil:
		// The client left: no one reads the answer.
		return
	case errors.Is(err, vlatte.ErrMatchPanicked):
		err = &problemError{status: http.StatusBadRequest, code: _codeInvalidBody, detail: "the query: " + err.Error()}
		fallthrough
	default:
		fail(w, r, err)
		return
	}

	// An empty page is a list, not null.
	if list.Requests == nil {
		list.Requests = []*vlatte.JournalEntry{}
	}

	respond(w, r, http.StatusOK, list)
}

// requestFilter makes the filter of the request part of a query, v, with pattern, the server's protocol's. A server
// whose protocol has none takes no request part.
func requestFilter(v config.Value, pattern RequestPattern) (vlatte.EntryFilter, error) {
	if pattern == nil {
		return nil, config.Invalid(v, "this mock server's protocol takes no request part", "select by the other keys")
	}

	return pattern(v)
}

// pageOf returns the limit and before parameters of r, a page of GET /requests.
func pageOf(r *http.Request) (int, uint64, error) {
	limit, before := _requestsPage, uint64(0)

	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || !pageLimit(int64(n)) {
			return 0, 0, invalidParameter("limit", s, _limitRule)
		}

		limit = n
	}

	if s := r.URL.Query().Get("before"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, invalidParameter("before", s, "the next of a page")
		}

		before = n
	}

	return limit, before, nil
}

func invalidParameter(name, value, want string) *problemError {
	return &problemError{
		status: http.StatusBadRequest, code: _codeInvalidParameter,
		detail: fmt.Sprintf("the parameter %s=%q must be %s", name, value, want),
	}
}

// _limitRule says what pageLimit takes.
var _limitRule = fmt.Sprintf("a number from 0 to %d", _maxRequestsPage)

// pageLimit tells whether n is a limit of a page of requests.
func pageLimit(n int64) bool {
	return n >= 0 && n <= _maxRequestsPage
}

func checkLimit(v config.Value) error {
	if !pageLimit(v.Int) {
		return config.Invalid(v, "limit must be "+_limitRule, "page with before")
	}

	return nil
}

// queryMatcher declares a key of a query whose value is a matcher of a field.
func queryMatcher(key, what string, set func(c *requestQuery, m vlatte.Matcher)) config.Field[requestQuery] {
	return config.Field[requestQuery]{
		Key: key, Kind: config.Any, Usage: what + ": text, a number, or a matcher list such as [\"glob\", \"/users/*\"].",
		Set: func(c *requestQuery, v config.Value) error {
			m, err := matcherOf(v)
			set(c, m)

			return err
		},
	}
}

// matcherOf returns the matcher of a value of a query, as a mock file's, but a number or a bool matches an equal
// value, such as a status.
func matcherOf(v config.Value) (vlatte.Matcher, error) {
	switch v.Any.(type) {
	case bool, float64, int64, int, uint64:
		return match.Equal(v.Any), nil
	}

	return filterbuilder.MatcherOf(v)
}

func parseTime(v config.Value, dst *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(v.Str))
	if err != nil {
		return config.Invalid(v, v.Key+" must be a time in RFC 3339", "such as 2026-10-21T12:00:00Z")
	}

	*dst = t

	return nil
}
