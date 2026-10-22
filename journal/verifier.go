package journal

import (
	"context"
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
)

// _recentRequests is how many of the newest requests a failed verification lists.
const _recentRequests = 10

// RequestPatterns is what the server of a protocol implements to verify requests with its own request patterns:
// RequestPattern turns a builder of its mocks, such as one of the request part of a mock, into a filter of the
// entries of its journal. A builder's reply, and what else matches no request, such as Times, are no part of it.
type RequestPatterns[TM vlatte.Mock, A any] interface {
	RequestPattern(b vlatte.Builder[TM, A]) (Filter, error)
}

// Verifier verifies the requests the journal of a mock server keeps, as WireMock's verify does, with the request
// patterns of the server's protocol. A server embeds it, next to its vlatte.Base, so every protocol verifies the same
// way.
type Verifier[TM vlatte.Mock, A any] struct {
	journal  *Journal
	patterns RequestPatterns[TM, A]
}

// NewVerifier returns the verifier of the requests j keeps, nil for a server that keeps none, whose patterns, nil for
// a protocol that has none, turn builders of the server's mocks into filters of j's entries.
func NewVerifier[TM vlatte.Mock, A any](j *Journal, patterns RequestPatterns[TM, A]) *Verifier[TM, A] {
	return &Verifier[TM, A]{journal: j, patterns: patterns}
}

// Journal returns the journal of the requests the server serves, nil when it keeps none.
func (v *Verifier[TM, A]) Journal() *Journal {
	return v.journal
}

// Requests returns the requests of the journal that q selects, and that every pattern matches, the newest first:
// all of them unless q has a Limit. A pattern is a builder of the server's mocks, such as mockhttp.Request(), of
// which only the request part counts. Journal().Find returns them a page at a time.
func (v *Verifier[TM, A]) Requests(ctx context.Context, q Query, patterns ...vlatte.Builder[TM, A]) ([]*Entry, error) {
	page, err := v.find(ctx, q, patterns)
	return page.Entries, err
}

// CountRequests returns how many requests of the journal q selects, and every pattern matches. See Requests.
func (v *Verifier[TM, A]) CountRequests(ctx context.Context, q Query, patterns ...vlatte.Builder[TM, A]) (int, error) {
	filters, err := v.filters(patterns)
	if err != nil {
		return 0, err
	}

	return v.journal.Count(ctx, q, filters...)
}

// AssertRequested asserts that the server served exactly expected requests that q selects, and that every pattern
// matches, whatever their outcome: a request no mock matched counts too. See Requests. On a failure, it lists the
// newest of those requests, or, when there are none, the newest the server served.
func (v *Verifier[TM, A]) AssertRequested(t vlatte.TestingT, expected int, q Query, patterns ...vlatte.Builder[TM, A]) bool {
	t.Helper()

	return v.assertRequests(t, q, patterns, func(count int) bool { return count == expected },
		func(count int) string {
			return fmt.Sprintf("Expected %d request(s) matching the query.\nGot %d.", expected, count)
		})
}

// AssertNotRequested asserts that the server served no request that q selects, and that every pattern matches. See
// Requests. On a failure, it lists the newest of those requests.
func (v *Verifier[TM, A]) AssertNotRequested(t vlatte.TestingT, q Query, patterns ...vlatte.Builder[TM, A]) bool {
	t.Helper()

	return v.assertRequests(t, q, patterns, func(count int) bool { return count == 0 },
		func(count int) string { return fmt.Sprintf("%d request(s) matched the query when none should.", count) })
}

// assertRequests asserts that the count of the requests q selects, with patterns, passes, or fails t with what
// failed says of it, and a list of requests. An assertion runs with a context of its own: one in a cleanup of t
// runs once t's context is done.
func (v *Verifier[TM, A]) assertRequests(t vlatte.TestingT, q Query, patterns []vlatte.Builder[TM, A], passes func(int) bool, failed func(int) string) bool {
	t.Helper()

	ctx := context.Background()
	q.Limit, q.Before = _recentRequests, 0

	page, err := v.find(ctx, q, patterns)
	if err != nil {
		t.Errorf("\nCould not verify the requests.\n%v", err)
		return false
	}

	if passes(page.Count) {
		return true
	}

	// None selected: what the server served instead tells why.
	listed, what := page, "matching the query"
	if page.Count == 0 {
		listed, err = v.journal.Find(ctx, Query{Limit: _recentRequests})
		what = "the server served"

		if err != nil {
			t.Errorf("\n%s\nCould not list the requests.\n%v", failed(page.Count), err)
			return false
		}
	}

	t.Errorf("\n%s\n%s", failed(page.Count), describeRequests(listed, what))

	return false
}

// find runs q, with the filters of patterns.
func (v *Verifier[TM, A]) find(ctx context.Context, q Query, patterns []vlatte.Builder[TM, A]) (Page, error) {
	filters, err := v.filters(patterns)
	if err != nil {
		return Page{}, err
	}

	return v.journal.Find(ctx, q, filters...)
}

// filters returns the filters of patterns, which the server's protocol makes. A server without a journal fails
// first: it can't verify, patterns or not.
func (v *Verifier[TM, A]) filters(patterns []vlatte.Builder[TM, A]) ([]Filter, error) {
	if v.journal == nil {
		return nil, ErrNoJournal
	}

	if len(patterns) == 0 {
		return nil, nil
	}

	if v.patterns == nil {
		return nil, ErrNoRequestPatterns
	}

	filters := make([]Filter, len(patterns))

	for i, pattern := range patterns {
		filter, err := v.patterns.RequestPattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("journal: pattern %d: %w", i, err)
		}

		filters[i] = filter
	}

	return filters, nil
}

// describeRequests describes the requests of page, what they are, one per line, for an assertion's message.
func describeRequests(page Page, what string) string {
	if len(page.Entries) == 0 {
		return "  Requests " + what + ": none\n"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "  Requests %s (%d of %d, the newest first):\n", what, len(page.Entries), page.Count)

	for _, e := range page.Entries {
		b.WriteString("    ")
		b.WriteString(describeEntry(e))
		b.WriteString("\n")
	}

	return b.String()
}

// describeEntry describes e in a line: its number, time, outcome, method, target and status, the mock that matched
// it, or the closest one that didn't, and what failed.
func describeEntry(e *Entry) string {
	var b strings.Builder

	fmt.Fprintf(&b, "#%d %s %s", e.Seq, e.Time.Format("2006-01-02T15:04:05.000Z07:00"), e.Outcome)

	for _, part := range []string{e.Method, e.Target} {
		if part != "" {
			b.WriteString(" ")
			b.WriteString(part)
		}
	}

	fmt.Fprintf(&b, " %d", e.Status)

	if e.MockID != "" {
		fmt.Fprintf(&b, " [%s]", e.MockID)
	}

	if e.BodyTruncated {
		b.WriteString(" (body cut)")
	}

	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}

	if len(e.NearMisses) > 0 {
		miss := e.NearMisses[0]
		fmt.Fprintf(&b, ", closest [%s]", miss.MockID)

		if len(miss.Failed) > 0 {
			b.WriteString(" differs in ")
			b.WriteString(strings.Join(miss.Failed, ", "))
		} else if len(miss.Skipped) > 0 {
			b.WriteString(" missed by ")
			b.WriteString(strings.Join(miss.Skipped, ", "))
		}
	}

	return b.String()
}
