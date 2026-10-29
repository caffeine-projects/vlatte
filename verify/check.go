package verify

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
)

var _ Journal = (*journal.Journal)(nil)

// Journal is the journal of requests a check reads, such as a *journal.Journal.
type Journal interface {
	Find(ctx context.Context, q journal.Query, filters ...journal.Filter) (journal.Page, error)
	Count(ctx context.Context, q journal.Query, filters ...journal.Filter) (int, error)
	Last() uint64
	Cleared() uint64
	Trimmed() uint64
	Changed() <-chan struct{}
}

// Spec is a check of the requests of a journal: those its query, outcomes and patterns select, counted against Count,
// now or over a wait. [Requests] fills one for a test; the admin API fills one for a client.
type Spec struct {
	Journal Journal

	// Query selects by the fields every protocol has. Its After is the mark the check counts from, which Marked says
	// is one; its Limit and Before must be 0, as a check counts all it selects.
	Query  journal.Query
	Marked bool

	// Outcomes selects the requests of any of them; none selects by Query's Outcome, if any.
	Outcomes []journal.Outcome

	// Patterns select by the request part of the server's mocks.
	Patterns []Pattern

	// Count matches the number of requests selected, an int. Expect says it in words for the report, such as
	// "exactly 1".
	Count  vlatte.Matcher
	Expect string

	// Within waits for Count to pass, up to its length; During waits its length for Count to fail. Neither checks now.
	Within time.Duration
	During time.Duration
}

// Result is what [Check] found. Waited is how long a check with a wait waited. Selected and Closest list requests only
// when the check failed, the newest first: those selected, and, when Count wanted more, those that came closest to
// being selected.
type Result struct {
	Passed   bool          `json:"passed"`
	Count    int           `json:"count"`
	Waited   time.Duration `json:"-"`
	Selected []Listed      `json:"selected,omitempty"`
	Closest  []Listed      `json:"closest,omitempty"`

	// Report says why the check failed, in lines, for a test's failure.
	Report string `json:"report,omitempty"`
}

// Listed is a request a [Result] lists: its sequence number, request id and report line, which leaves out its headers
// and body, and, for one that came close, the parts it passed of those checked and the names of those it missed.
type Listed struct {
	Seq    uint64   `json:"seq"`
	ID     string   `json:"id"`
	Line   string   `json:"line"`
	Passed int      `json:"passed,omitzero"`
	Total  int      `json:"total,omitzero"`
	Missed []string `json:"missed,omitempty"`
}

const (
	// _listed is how many requests a result lists of each kind, and _scanned how many of the newest it ranks to find
	// the closest.
	_listed  = 10
	_scanned = 100

	// _wantsMore is how far past its count a check looks for a count Count passes, to tell it wanted more requests.
	_wantsMore = 1000
)

// Check runs s: it counts the requests s selects and matches Count with the count, now, or on each change of the
// journal until Within or During ends, without polling. A check that can't count, as when the journal dropped
// requests it would count, fails with an error rather than pass or fail on part of them: [ErrDropped], or
// [ErrCleared] for a journal cleared after the mark or during the wait. A journal closed during the wait fails it with
// [journal.ErrClosed].
func Check(ctx context.Context, s Spec) (Result, error) {
	if err := s.check(); err != nil {
		return Result{}, err
	}

	s.foldOutcome()

	filters := s.filters()
	wait := max(s.Within, s.During)
	start, cleared := time.Now(), s.Journal.Cleared()

	var deadline <-chan time.Time

	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		deadline = timer.C
	}

	for final := wait == 0; ; {
		changed := s.Journal.Changed()

		count, err := s.count(ctx, filters)
		if err != nil {
			return Result{}, err
		}

		// A clear during the wait took what the check had counted.
		if s.Journal.Cleared() != cleared {
			return Result{}, ErrCleared
		}

		passed, err := countPasses(ctx, s.Count, count)
		if err != nil {
			return Result{}, err
		}

		switch {
		case s.During > 0 && !passed, s.Within > 0 && passed, final:
			r := Result{Passed: passed, Count: count}
			if wait > 0 {
				r.Waited = time.Since(start)
			}

			if !passed {
				err = s.report(ctx, filters, &r)
			}

			return r, err
		}

		select {
		case <-changed:
		case <-deadline:
			final = true
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
}

// check tells whether s can run.
func (s *Spec) check() error {
	if err := s.checkSelection(); err != nil {
		return err
	}

	switch {
	case s.Count == nil:
		return fmt.Errorf("%w: no count to match", ErrInvalidSpec)
	case s.Within < 0 || s.During < 0:
		return fmt.Errorf("%w: a wait must be longer than 0", ErrInvalidSpec)
	case s.Within > 0 && s.During > 0:
		return fmt.Errorf("%w: it waits Within or During, not both", ErrInvalidSpec)
	}

	if s.Within > 0 {
		// A count that passes with no request passes at once: the wait would wait for nothing.
		passed, err := countPasses(context.Background(), s.Count, 0)
		if err != nil {
			return err
		}

		if passed {
			return fmt.Errorf("%w: %s request(s) passes with none, so Within would pass at once: use During", ErrInvalidSpec, s.Expect)
		}
	}

	return nil
}

// foldOutcome makes the one outcome s selects by, if one, the Outcome of its query, which the journal indexes.
func (s *Spec) foldOutcome() {
	if len(s.Outcomes) == 1 {
		s.Query.Outcome, s.Outcomes = s.Outcomes[0], nil
	}
}

// checkSelection tells whether s can select the requests it checks.
func (s *Spec) checkSelection() error {
	switch {
	case s.Journal == nil:
		return journal.ErrNoJournal
	case s.Query.Limit != 0 || s.Query.Before != 0:
		return fmt.Errorf("%w: a check counts all it selects: its query has no Limit or Before", ErrInvalidSpec)
	}

	for _, o := range s.Outcomes {
		if !o.Valid() {
			return fmt.Errorf("%w: outcome %d is not one of matched, unmatched, proxied, failed or abandoned", ErrInvalidSpec, o)
		}
	}

	return nil
}

// filters returns the filters of s's outcomes and patterns.
func (s *Spec) filters() []journal.Filter {
	var filters []journal.Filter

	if len(s.Outcomes) > 0 {
		filters = append(filters, func(_ context.Context, e *journal.Entry) (bool, error) {
			return slices.Contains(s.Outcomes, e.Outcome), nil
		})
	}

	for _, p := range s.Patterns {
		filters = append(filters, p.Match)
	}

	return filters
}

// count returns how many requests s selects, unless the journal no longer holds all those it would count: a clear
// after the mark or as it counted, or entries dropped to keep MaxEntries, removed some.
func (s *Spec) count(ctx context.Context, filters []journal.Filter) (int, error) {
	cleared := s.Journal.Cleared()

	count, err := s.Journal.Count(ctx, s.Query, filters...)
	if err != nil {
		return 0, err
	}

	if err := s.whole(cleared); err != nil {
		return 0, err
	}

	return count, nil
}

// whole tells whether the journal held every request s would select as it read them, once it read them: cleared is
// where its last clear was before it read. A clear after the mark or as it read, or entries dropped to keep
// MaxEntries, may have removed some, and a mark past the journal's newest request is of a journal that started over.
func (s *Spec) whole(cleared uint64) error {
	// Trimmed first: a clear that comes after it, which starts the trim over, moves Cleared.
	trimmed := s.Journal.Trimmed()

	switch {
	case s.Journal.Cleared() != cleared:
		return ErrCleared
	case s.Marked && (s.Query.After < cleared || s.Query.After > s.Journal.Last()):
		return ErrCleared
	}

	if from := max(s.Query.After, cleared); trimmed > from {
		return fmt.Errorf("%w: %d of them", ErrDropped, trimmed-from)
	}

	return nil
}

// countPasses tells whether m matches count, the number of requests a check selects. A matcher that fails, or panics,
// fails the check.
func countPasses(ctx context.Context, m vlatte.Matcher, count int) (bool, error) {
	eval := vlatte.Eval[int]{Key: "count", Matcher: m, Selector: func(_ context.Context, n int) any { return n }}

	ok, err := eval.Match(ctx, nil, count)
	if err != nil {
		return false, fmt.Errorf("verify: %w: count: %w", vlatte.ErrMatching, err)
	}

	return ok, nil
}
