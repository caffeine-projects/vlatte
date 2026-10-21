package vlatte

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// _recentRequests is how many of the newest requests a failed verification lists.
const _recentRequests = 10

// RequestPatterns is what the server of a protocol implements to verify requests with its own request patterns:
// RequestPattern turns a builder of its mocks, such as one of the request part of a mock, into a filter of the
// entries of its journal. A builder's reply, and what else matches no request, such as Times, are no part of it.
type RequestPatterns[TM Mock, A any] interface {
	RequestPattern(b Builder[TM, A]) (EntryFilter, error)
}

type Base[TM Mock, A any] struct {
	scopes  *Scopes[TM, A]
	journal *Journal
	mu      sync.RWMutex
}

// NewBase returns the base of app, a mock server whose mocks repo stores, and whose journal, nil for none, keeps the
// requests it serves.
func NewBase[TM Mock, A any](app A, repo Store[TM], journal *Journal) *Base[TM, A] {
	return &Base[TM, A]{scopes: &Scopes[TM, A]{flavor: app, store: repo}, journal: journal}
}

// Mock adds one or multiple request mocks. A mock stored outside every scope, such as one the admin API added, gives
// way to a new mock with its ID.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
func (app *Base[TM, A]) Mock(builders ...Builder[TM, A]) (*Scope[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.Add(builders...)
}

// MustMock adds one or multiple request mocks.
// It returns a Scope instance that allows control of the added mocks and also checks if they were called or not.
// It panics if any error occurs.
func (app *Base[TM, A]) MustMock(builders ...Builder[TM, A]) *Scope[TM] {
	scope, err := app.Mock(builders...)
	if err != nil {
		panic(err)
	}

	return scope
}

// Remock builds builders and swaps their mocks for those of old in one step, so a request finds the mocks of old or
// the new ones, never neither, and returns their scope, which takes the place of old. With a nil old, it adds the
// mocks, as Mock does. A mock stored outside every scope, such as one the admin API added, gives way to a new mock with
// its ID. It changes nothing when a builder fails, when the store refuses the mocks, for example over an ID in use in
// another scope, or when old isn't tracked: replaced or unmocked already, or another server's.
func (app *Base[TM, A]) Remock(ctx context.Context, old *Scope[TM], builders ...Builder[TM, A]) (*Scope[TM], error) {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.Replace(ctx, old, builders...)
}

// Unmock deletes the mocks of scope and stops tracking it, so Hits, the mock lists and the assertions leave it out.
func (app *Base[TM, A]) Unmock(ctx context.Context, scope *Scope[TM]) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	return app.scopes.Remove(ctx, scope)
}

// Hits returns the total matched request hits.
func (app *Base[TM, A]) Hits() int64 {
	hits := int64(0)

	for _, s := range app.scopes.All() {
		hits += s.Hits()
	}

	return hits
}

// Enable enables all mocks.
func (app *Base[TM, A]) Enable() {
	app.mu.Lock()
	defer app.mu.Unlock()

	for _, scope := range app.scopes.All() {
		scope.Enable()
	}
}

// Disable disables all mocks.
func (app *Base[TM, A]) Disable() {
	for _, scope := range app.scopes.All() {
		scope.Disable()
	}
}

func (app *Base[TM, A]) ListPending() []TM {
	scopes := app.scopes.All()
	ret := make([]TM, 0, len(scopes)/2)
	for _, scope := range scopes {
		ret = append(ret, scope.ListPending()...)
	}

	return ret
}

func (app *Base[TM, A]) ListCalled() []TM {
	scopes := app.scopes.All()
	ret := make([]TM, 0, len(scopes)/2)
	for _, scope := range scopes {
		ret = append(ret, scope.ListCalled()...)
	}

	return ret
}

// Clean removes all scoped mocks.
func (app *Base[TM, A]) Clean(ctx context.Context) error {
	app.mu.Lock()
	defer app.mu.Unlock()

	for i, scope := range app.scopes.All() {
		err := scope.Clear(ctx)
		if err != nil {
			return fmt.Errorf("error cleaning mocks from scope %d. %w", i, err)
		}
	}

	return nil
}

// --
// Assertions
// --

// AssertCalled asserts that all mocks associated with this instance were called at least once.
func (app *Base[TM, A]) AssertCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, scope := range app.scopes.All() {
		if scope.Pending() {
			fmt.Fprintf(&buf, "   Scope %d\n", i)
			pending := scope.ListPending()
			size += len(pending)

			for _, p := range pending {
				buf.WriteString("    Mock [")
				buf.WriteString(p.ID())
				buf.WriteString("] ")
				buf.WriteString(p.Name())
				buf.WriteString("\n")
			}

			result = false
		}
	}

	if !result {
		t.Errorf("\nThere are still %d mock(s) that were not called.\n  Pending:\n%s",
			size,
			buf.String(),
		)
	}

	return result
}

// AssertNotCalled asserts that none of the mocks associated with this instance were called.
func (app *Base[TM, A]) AssertNotCalled(t TestingT) bool {
	t.Helper()

	result := true
	size := 0
	buf := strings.Builder{}

	for i, scope := range app.scopes.All() {
		called := scope.ListCalled()
		if len(called) == 0 {
			continue
		}

		fmt.Fprintf(&buf, "   Scope %d\n", i)
		size += len(called)

		for _, p := range called {
			buf.WriteString("    Mock [")
			buf.WriteString(p.ID())
			buf.WriteString("] ")
			buf.WriteString(p.Name())
			buf.WriteString("\n")
		}

		result = false
	}

	if !result {
		t.Errorf(
			"\n%d Mock(s) were called at least once when none should be.\n  Called:\n%s",
			size,
			buf.String(),
		)
	}

	return result
}

// AssertNumberOfCalls asserts that the sum of matched request hits
// is equal to the given expected value.
func (app *Base[TM, A]) AssertNumberOfCalls(t TestingT, expected int64) bool {
	t.Helper()

	hits := app.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hit(s).\n Got %d", expected, hits)

	return false
}

// --
// Journal
// --

// Journal returns the journal of the requests the server serves, nil when it keeps none.
func (app *Base[TM, A]) Journal() *Journal {
	return app.journal
}

// Requests returns the requests of the journal that q selects, and that every pattern matches, the newest first:
// all of them unless q has a Limit. A pattern is a builder of the server's mocks, such as mockhttp.Request(), of
// which only the request part counts. Journal().Find returns them a page at a time.
func (app *Base[TM, A]) Requests(ctx context.Context, q JournalQuery, patterns ...Builder[TM, A]) ([]*JournalEntry, error) {
	page, err := app.find(ctx, q, patterns)
	return page.Entries, err
}

// CountRequests returns how many requests of the journal q selects, and every pattern matches. See Requests.
func (app *Base[TM, A]) CountRequests(ctx context.Context, q JournalQuery, patterns ...Builder[TM, A]) (int, error) {
	filters, err := app.patterns(patterns)
	if err != nil {
		return 0, err
	}

	return app.journal.Count(ctx, q, filters...)
}

// AssertRequested asserts that the server served exactly expected requests that q selects, and that every pattern
// matches, whatever their outcome: a request no mock matched counts too. See Requests. On a failure, it lists the
// newest of those requests, or, when there are none, the newest the server served.
func (app *Base[TM, A]) AssertRequested(t TestingT, expected int, q JournalQuery, patterns ...Builder[TM, A]) bool {
	t.Helper()

	return app.assertRequests(t, q, patterns, func(count int) bool { return count == expected },
		func(count int) string {
			return fmt.Sprintf("Expected %d request(s) matching the query.\nGot %d.", expected, count)
		})
}

// AssertNotRequested asserts that the server served no request that q selects, and that every pattern matches. See
// Requests. On a failure, it lists the newest of those requests.
func (app *Base[TM, A]) AssertNotRequested(t TestingT, q JournalQuery, patterns ...Builder[TM, A]) bool {
	t.Helper()

	return app.assertRequests(t, q, patterns, func(count int) bool { return count == 0 },
		func(count int) string { return fmt.Sprintf("%d request(s) matched the query when none should.", count) })
}

// assertRequests asserts that the count of the requests q selects, with patterns, passes, or fails t with what
// failed says of it, and a list of requests. An assertion runs with a context of its own: one in a cleanup of t
// runs once t's context is done.
func (app *Base[TM, A]) assertRequests(t TestingT, q JournalQuery, patterns []Builder[TM, A], passes func(int) bool, failed func(int) string) bool {
	t.Helper()

	ctx := context.Background()
	q.Limit, q.Before = _recentRequests, 0

	page, err := app.find(ctx, q, patterns)
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
		listed, err = app.journal.Find(ctx, JournalQuery{Limit: _recentRequests})
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
func (app *Base[TM, A]) find(ctx context.Context, q JournalQuery, patterns []Builder[TM, A]) (JournalPage, error) {
	filters, err := app.patterns(patterns)
	if err != nil {
		return JournalPage{}, err
	}

	return app.journal.Find(ctx, q, filters...)
}

// patterns returns the filters of patterns, which the server's protocol makes. A server without a journal fails
// first: it can't verify, patterns or not.
func (app *Base[TM, A]) patterns(patterns []Builder[TM, A]) ([]EntryFilter, error) {
	if app.journal == nil {
		return nil, ErrNoJournal
	}

	if len(patterns) == 0 {
		return nil, nil
	}

	protocol, ok := any(app.scopes.flavor).(RequestPatterns[TM, A])
	if !ok {
		return nil, ErrNoRequestPatterns
	}

	filters := make([]EntryFilter, len(patterns))

	for i, pattern := range patterns {
		filter, err := protocol.RequestPattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("journal: pattern %d: %w", i, err)
		}

		filters[i] = filter
	}

	return filters, nil
}

// describeRequests describes the requests of page, what they are, one per line, for an assertion's message.
func describeRequests(page JournalPage, what string) string {
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
func describeEntry(e *JournalEntry) string {
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
