package verify

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
)

// Requests is a selection of the requests of a server's journal, which [Verifier.VerifyRequests] starts. Its criteria
// narrow it: all must match. A selection is a value: each method returns a copy, so one is safe to name, reuse and
// share. A criterion it can't take, or a server with no journal, fails the check, or the read, that ends it.
type Requests[TM vlatte.Mock, A any] struct {
	v        *Verifier[TM, A]
	q        journal.Query
	outcomes []journal.Outcome
	patterns []vlatte.Builder[TM, A]
	mark     *Mark
	within   time.Duration
	during   time.Duration
	err      error
}

// Method selects the requests of any of methods, in any case, such as "POST". gRPC calls have none: Method fails on a
// gRPC server with [ErrNoMethod].
func (r Requests[TM, A]) Method(methods ...string) Requests[TM, A] {
	switch {
	case len(methods) == 0:
		return r.fail(errors.New("verify: Method needs a method"))
	case r.v.protocol.NoMethod:
		return r.fail(ErrNoMethod)
	}

	matchers := make([]vlatte.Matcher, len(methods))
	for i, m := range methods {
		matchers[i] = match.EqualIgnoreCase(m)
	}

	r.q.Method = matchers[0]
	if len(matchers) > 1 {
		r.q.Method = match.Any(matchers...)
	}

	return r
}

// Path selects the requests whose path m matches: the URL's path for HTTP, the full method for gRPC, such as
// "/helloworld.Greeter/SayHello".
func (r Requests[TM, A]) Path(m vlatte.Matcher) Requests[TM, A] {
	r.q.Path = m
	return r.need(m, "Path")
}

// Target selects the requests whose full target m matches, such as the URL with its query string.
func (r Requests[TM, A]) Target(m vlatte.Matcher) Requests[TM, A] {
	r.q.Target = m
	return r.need(m, "Target")
}

// Header selects the requests whose header named name, in any case, m matches: the matcher gets its first value, or
// "" for a request without it. gRPC calls have metadata. Each name adds a header to match.
func (r Requests[TM, A]) Header(name string, m vlatte.Matcher) Requests[TM, A] {
	r.q.Header = maps.Clone(r.q.Header)
	if r.q.Header == nil {
		r.q.Header = make(map[string]vlatte.Matcher)
	}

	r.q.Header[name] = m

	return r.need(m, "Header "+name)
}

// Body selects the requests whose body, as text, m matches, such as with [match.JSONPath]. A gRPC call's body is its
// request message as JSON, or the array of a client stream's messages. A check that selects a request whose body the
// journal cut fails with [journal.ErrBodyCut] and [vlatte.ErrMatching]: what was cut may hold what m looks for.
func (r Requests[TM, A]) Body(m vlatte.Matcher) Requests[TM, A] {
	r.q.Body = m
	return r.need(m, "Body")
}

// Status selects the requests whose answer had a status m matches, an int: an HTTP status, or a gRPC code.
func (r Requests[TM, A]) Status(m vlatte.Matcher) Requests[TM, A] {
	r.q.Status = m
	return r.need(m, "Status")
}

// Outcome selects the requests of any of outcomes, such as journal.OutcomeUnmatched.
func (r Requests[TM, A]) Outcome(outcomes ...journal.Outcome) Requests[TM, A] {
	if len(outcomes) == 0 {
		return r.fail(errors.New("verify: Outcome needs an outcome"))
	}

	r.outcomes = slices.Clone(outcomes)

	return r
}

// MockID selects the requests a mock whose id m matches answered.
func (r Requests[TM, A]) MockID(m vlatte.Matcher) Requests[TM, A] {
	r.q.MockID = m
	return r.need(m, "MockID")
}

// Matching selects the requests the request part of every pattern matches, as it matches a request: a pattern is a
// builder of the server's mocks, such as mockhttp.Request().FormField("a", match.Equal("1")), for what no other
// criterion says, such as an HTTP query parameter, form field or file. Its reply counts for nothing, and one with
// Times, a scenario, or a part the journal doesn't keep fails the check.
func (r Requests[TM, A]) Matching(patterns ...vlatte.Builder[TM, A]) Requests[TM, A] {
	r.patterns = append(slices.Clip(r.patterns), patterns...)
	return r
}

// Since selects the requests that came after mark, which [Verifier.JournalMark] took: none of those before, such as
// another test's on a server they share. A mark isolates tests that run one after the other: two tests on one server
// at the same time see each other's requests after their marks. A mark of another server fails the check with
// [ErrOtherJournal], and one the journal was cleared after with [ErrCleared].
func (r Requests[TM, A]) Since(mark Mark) Requests[TM, A] {
	r.mark = &mark
	return r
}

// Within makes the check wait up to d for its count to pass, such as for a request the code under test sends in the
// background: it passes once the count does, rechecking as each request comes, and fails when d ends. A count that
// passes with no request, such as Never's, fails the check: use During. A clear of the journal during the wait fails
// the check with [ErrCleared], and a server that closes with [journal.ErrClosed].
func (r Requests[TM, A]) Within(d time.Duration) Requests[TM, A] {
	if d <= 0 {
		return r.fail(fmt.Errorf("verify: Within must be longer than 0, not %s", d))
	}

	r.within = d

	return r
}

// During makes the check hold for d: it fails once the count fails, rechecking as each request comes, and passes
// when d ends, such as for Never to tell that no request came in that time. It ends as Within does on a clear or a
// close.
func (r Requests[TM, A]) During(d time.Duration) Requests[TM, A] {
	if d <= 0 {
		return r.fail(fmt.Errorf("verify: During must be longer than 0, not %s", d))
	}

	r.during = d

	return r
}

// Times asserts that the selection holds exactly n requests. On a failure, it reports with t.Errorf the requests it
// holds and, when it holds fewer, those that came closest, with the parts they missed, and returns false.
func (r Requests[TM, A]) Times(t vlatte.TestingT, n int) bool {
	t.Helper()
	return r.assert(t, n, match.Equal(n), fmt.Sprintf("exactly %d", n))
}

// AtLeast asserts that the selection holds n requests or more. See Times.
func (r Requests[TM, A]) AtLeast(t vlatte.TestingT, n int) bool {
	t.Helper()
	return r.assert(t, n, match.GTE(n), fmt.Sprintf("at least %d", n))
}

// AtMost asserts that the selection holds n requests or fewer. See Times.
func (r Requests[TM, A]) AtMost(t vlatte.TestingT, n int) bool {
	t.Helper()
	return r.assert(t, n, match.LTE(n), fmt.Sprintf("at most %d", n))
}

// Never asserts that the selection holds no request. See Times.
func (r Requests[TM, A]) Never(t vlatte.TestingT) bool {
	t.Helper()
	return r.assert(t, 0, match.Equal(0), "no")
}

// CountIs asserts that count, a matcher of an int, matches the number of requests the selection holds, such as
// match.LTE(3). See Times.
func (r Requests[TM, A]) CountIs(t vlatte.TestingT, count vlatte.Matcher) bool {
	t.Helper()

	if count == nil {
		return r.fail(errors.New("verify: CountIs needs a matcher")).assert(t, 0, nil, "")
	}

	return r.assert(t, 0, count, "a number matching "+count.Name()+" of")
}

// Find returns the requests the selection holds, the newest first, with what the journal kept of each: the selection
// has no limit, so [journal.Journal.Find] pages them instead. It neither waits nor checks: Within and During count for
// nothing. It fails, as a check does, rather than return part of the requests: with [ErrDropped] or [ErrCleared].
func (r Requests[TM, A]) Find(ctx context.Context) ([]*journal.Entry, error) {
	s, err := r.spec(nil, "")
	if err != nil {
		return nil, err
	}

	cleared := s.Journal.Cleared()

	page, err := s.Journal.Find(ctx, s.Query, s.filters()...)
	if err != nil {
		return nil, err
	}

	// Checked once the entries are found, as a check does: what the journal dropped while it read counts too.
	if err := s.whole(cleared); err != nil {
		return nil, err
	}

	return page.Entries, nil
}

// Count returns how many requests the selection holds. See Find.
func (r Requests[TM, A]) Count(ctx context.Context) (int, error) {
	s, err := r.spec(nil, "")
	if err != nil {
		return 0, err
	}

	return s.count(ctx, s.filters())
}

// assert checks that count, the matcher of n, passes, and reports with t what failed. A check runs with a context of
// its own: one in a cleanup of t runs once t's context is done.
func (r Requests[TM, A]) assert(t vlatte.TestingT, n int, count vlatte.Matcher, expect string) bool {
	t.Helper()

	if n < 0 {
		r = r.fail(fmt.Errorf("verify: a count must be 0 or more, not %d", n))
	}

	s, err := r.spec(count, expect)
	if err == nil {
		var result Result
		if result, err = Check(context.Background(), s); err == nil {
			if !result.Passed {
				t.Errorf("%s", result.Report)
			}

			return result.Passed
		}
	}

	t.Errorf("\nCould not verify the requests.\n%v", err)

	return false
}

// spec returns the check of the selection, with count, a matcher of its number of requests, which expect says.
func (r Requests[TM, A]) spec(count vlatte.Matcher, expect string) (Spec, error) {
	if r.err != nil {
		return Spec{}, r.err
	}

	if r.v.j == nil {
		return Spec{}, fmt.Errorf("%w: set Setup().Journal(\"\")", journal.ErrNoJournal)
	}

	s := Spec{
		Journal:  r.v.j,
		Query:    r.q,
		Outcomes: r.outcomes,
		Count:    count,
		Expect:   expect,
		Within:   r.within,
		During:   r.during,
	}

	s.foldOutcome()

	if r.mark != nil {
		if r.mark.j != r.v.j {
			return Spec{}, ErrOtherJournal
		}

		s.Query.After, s.Marked = r.mark.seq, true
	}

	if len(r.patterns) > 0 && r.v.protocol.Patterns == nil {
		return Spec{}, ErrNoRequestPatterns
	}

	for i, b := range r.patterns {
		p, err := r.v.protocol.Patterns.RequestPattern(b)
		if err != nil {
			return Spec{}, fmt.Errorf("verify: pattern %d: %w", i, err)
		}

		s.Patterns = append(s.Patterns, p)
	}

	return s, nil
}

// need fails the selection when m, the matcher of a criterion, is nil.
func (r Requests[TM, A]) need(m vlatte.Matcher, criterion string) Requests[TM, A] {
	if m == nil {
		return r.fail(fmt.Errorf("verify: %s needs a matcher", strings.TrimSpace(criterion)))
	}

	return r
}

// fail makes err the error of the selection, unless it has one: the first one counts.
func (r Requests[TM, A]) fail(err error) Requests[TM, A] {
	if r.err == nil {
		r.err = err
	}

	return r
}
