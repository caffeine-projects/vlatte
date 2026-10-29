package handler

import (
	"context"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/verify"
)

// requestQuery is the body of QUERY /requests: a query, the request part of a mock, and a page. As the body of QUERY
// /requests/verify, it is a check instead: a query, the request part of a mock, a count and a wait, or the steps of an
// order.
type requestQuery struct {
	matchers *matchbuild.Registry // builds the matcher lists of the query: the server's
	q        journal.Query
	request  *config.Value
	limit    int

	marked bool // whether the check names its mark, its after
	count  vlatte.Matcher
	expect string // says count in words, as a report does
	within time.Duration
	during time.Duration
	steps  []requestQuery
}

var _requestQuerySchema = config.NewSchema(append(selectionFields(),
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
	config.Field[requestQuery]{
		Key: "after", Kind: config.Int, Usage: "Answer the requests after this one, such as the mark of a list.",
		Set: func(c *requestQuery, v config.Value) error { return setAfter(&c.q, v) },
	},
)...)

// selectionFields declares the keys of a query that select requests: those of QUERY /requests, and of each step of
// an order QUERY /requests/verify checks.
func selectionFields() []config.Field[requestQuery] {
	return []config.Field[requestQuery]{
		{
			Key: "kind", Kind: config.String, Usage: "The protocol of the requests, such as http.",
			Set: func(c *requestQuery, v config.Value) error { c.q.Kind = vlatte.Kind(v.Str); return nil },
		},
		{
			Key: "outcome", Kind: config.Int, Usage: "What came of the requests: matched, unmatched, proxied, failed or abandoned.",
			Choices: []config.Choice{
				{Name: "matched", Value: int64(journal.OutcomeMatched)},
				{Name: "unmatched", Value: int64(journal.OutcomeUnmatched)},
				{Name: "proxied", Value: int64(journal.OutcomeProxied)},
				{Name: "failed", Value: int64(journal.OutcomeFailed)},
				{Name: "abandoned", Value: int64(journal.OutcomeAbandoned)},
			},
			Set: func(c *requestQuery, v config.Value) error {
				c.q.Outcome = journal.Outcome(v.Int)
				return nil
			},
		},
		queryMatcher("mock_id", "The mock that matched", func(c *requestQuery, m vlatte.Matcher) { c.q.MockID = m }),
		queryMatcher("method", "The method", func(c *requestQuery, m vlatte.Matcher) { c.q.Method = m }),
		queryMatcher("path", "The path", func(c *requestQuery, m vlatte.Matcher) { c.q.Path = m }),
		queryMatcher("target", "What the requests asked for in full, such as the URL", func(c *requestQuery, m vlatte.Matcher) { c.q.Target = m }),
		queryMatcher("status", "The status of the answer", func(c *requestQuery, m vlatte.Matcher) { c.q.Status = m }),
		queryMatcher("body", "The body, as text", func(c *requestQuery, m vlatte.Matcher) { c.q.Body = m }),
		{
			Key: "header", Kind: config.Any, Usage: "Headers, or metadata, by name, in any case: text or a matcher list each.",
			Set: func(c *requestQuery, v config.Value) error {
				c.q.Header = make(map[string]vlatte.Matcher)

				return config.Members(v, func(name string, member config.Value) error {
					m, err := matcherOf(c.matchers, member)
					c.q.Header[name] = m

					return err
				})
			},
		},
		{
			Key: "since", Kind: config.String, Usage: "The earliest time of the requests, in RFC 3339.",
			Set: func(c *requestQuery, v config.Value) error { return parseTime(v, &c.q.Since) },
		},
		{
			Key: "until", Kind: config.String, Usage: "The time the requests came before, in RFC 3339.",
			Set: func(c *requestQuery, v config.Value) error { return parseTime(v, &c.q.Until) },
		},
		{
			Key: "request", Kind: config.Any, Usage: "The request part of a mock of the server's protocol, as a mock file writes it.",
			Set: func(c *requestQuery, v config.Value) error { c.request = &v; return nil },
		},
	}
}

// setAfter sets the After of q to v, a mark.
func setAfter(q *journal.Query, v config.Value) error {
	if v.Int < 0 {
		return config.Invalid(v, "after must be 0 or more", "send the mark of a list")
	}

	q.After = uint64(v.Int)

	return nil
}

// QueryRequests returns the requests the query of doc, a document in format, selects, and their count. A request
// part, which the server's protocol makes a pattern of, narrows them as a mock matches. An empty document selects them
// all. It needs a server that keeps a journal.
func (o *Operations) QueryRequests(ctx context.Context, doc []byte, format string) (RequestList, error) {
	if o.journal == nil {
		return RequestList{}, notSupported(_lackingJournal)
	}

	query := requestQuery{matchers: o.matchers, limit: _requestsPage}
	if err := decodeBody(_requestQuerySchema, &query, doc, format); err != nil {
		return RequestList{}, invalid("the query", err)
	}

	var filters []journal.Filter

	if query.request != nil {
		pattern, err := requestPattern(*query.request, o.pattern)
		if err != nil {
			return RequestList{}, invalid("the query", err)
		}

		filters = append(filters, pattern.Match)
	}

	return o.findRequests(ctx, query.q, filters, query.limit)
}

// requestPattern makes the pattern of the request part of a query, v, with pattern, the server's protocol's. A server
// whose protocol has none takes no request part.
func requestPattern(v config.Value, pattern adminapi.RequestPattern) (verify.Pattern, error) {
	if pattern == nil {
		return nil, config.Invalid(v, "this mock server's protocol takes no request part", "select by the other keys")
	}

	return pattern(v)
}

// queryMatcher declares a key of a query whose value is a matcher of a field.
func queryMatcher(key, what string, set func(c *requestQuery, m vlatte.Matcher)) config.Field[requestQuery] {
	return config.Field[requestQuery]{
		Key: key, Kind: config.Any, Usage: what + ": text, a number, or a matcher list such as [\"glob\", \"/users/*\"].",
		Set: func(c *requestQuery, v config.Value) error {
			m, err := matcherOf(c.matchers, v)
			set(c, m)

			return err
		},
	}
}

// matcherOf returns the matcher of a value of a query, as a mock file's, but a number or a bool matches an equal
// value, such as a status.
func matcherOf(matchers *matchbuild.Registry, v config.Value) (vlatte.Matcher, error) {
	switch v.Any.(type) {
	case bool, float64, int64, int, uint64:
		return match.Equal(v.Any), nil
	}

	return matchers.TryBuildMatcher(v.Any)
}

func parseTime(v config.Value, dst *time.Time) error {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(v.Str))
	if err != nil {
		return config.Invalid(v, v.Key+" must be a time in RFC 3339", "such as 2026-10-21T12:00:00Z")
	}

	*dst = t

	return nil
}
