package journal

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/caffeine-projects/vlatte"
)

// Filter tells whether an entry of a journal is one a query wants, such as a protocol's request pattern does.
type Filter func(ctx context.Context, e *Entry) (bool, error)

// Query selects entries of a [Journal]. Each field set narrows the entries it selects; the zero query selects
// them all. A matcher gets the value of its field: text, or an int for Status.
type Query struct {
	Kind    vlatte.Kind
	Outcome Outcome

	MockID vlatte.Matcher
	Method vlatte.Matcher
	Path   vlatte.Matcher
	Target vlatte.Matcher
	Status vlatte.Matcher

	// Since and Until bound the time of the request: Since includes it, Until doesn't.
	Since time.Time
	Until time.Time

	// Header matches headers by name, in any case: a matcher gets the first value of its header, or "" when the
	// request has none.
	Header map[string]vlatte.Matcher

	// Body gets the body as text. A query the rest of which selects an entry whose body the journal cut fails with
	// [vlatte.ErrMatching]: what was cut may hold what Body looks for.
	Body vlatte.Matcher

	// Limit is the most entries Find returns, the newest first; 0 returns them all. Count leaves it out.
	Limit int

	// Before selects the entries with a lower sequence number, those that follow a page Find returned, which its Next
	// names. Count leaves it out.
	Before uint64

	// After selects the entries with a greater sequence number, those appended since the entry it names, such as the
	// newest one [Journal.Last] returned before a test sent its requests. Count selects by it too.
	After uint64
}

// Page is a page of the entries a query selects, the newest first. Count is the number of entries the query
// selects, its Limit and Before left out. Next, when not 0, is the Before of the next page.
type Page struct {
	Entries []*Entry
	Count   int
	Next    uint64
}

// journalSnapshot is what a query reads of a journal: what it held when the query began. It holds the values of the
// indexed fields the query matches: the query matches them without the journal's lock, then takes their lists.
type journalSnapshot struct {
	dir      string
	rows     []journalRow
	mockIDs  []string
	methods  []string
	paths    []string
	statuses []int
}

// journalAccepted holds the values of a snapshot that the matchers of a query accepted, by field.
type journalAccepted struct {
	mockIDs  []string
	methods  []string
	paths    []string
	statuses []int
}

// accept matches the values of s with the matchers of q: each matcher runs once per value, not once per entry.
func (s *journalSnapshot) accept(ctx context.Context, q *Query) (*journalAccepted, error) {
	var (
		a   journalAccepted
		err error
	)

	if a.mockIDs, err = acceptedValues(ctx, q.MockID, s.mockIDs); err != nil {
		return nil, err
	}

	if a.methods, err = acceptedValues(ctx, q.Method, s.methods); err != nil {
		return nil, err
	}

	if a.paths, err = acceptedValues(ctx, q.Path, s.paths); err != nil {
		return nil, err
	}

	if a.statuses, err = acceptedValues(ctx, q.Status, s.statuses); err != nil {
		return nil, err
	}

	return &a, nil
}

// candidates returns the rows of s that fields select, in order: fields holds, for each indexed field a query
// selects by, the lists of the values it accepts, which are joined, then intersected across the fields. No field
// selects every row.
func (s *journalSnapshot) candidates(fields [][][]uint64) []journalRow {
	if len(fields) == 0 {
		return s.rows
	}

	seqs := union(fields[0])
	for _, lists := range fields[1:] {
		seqs = intersect(seqs, union(lists))
	}

	rows := make([]journalRow, 0, len(seqs))
	lo := 0

	for _, seq := range seqs {
		i, found := searchRows(s.rows[lo:], seq)
		lo += i

		if found {
			rows = append(rows, s.rows[lo])
		}
	}

	return rows
}

// acceptedValues returns the values m matches. A nil m accepts none: its field doesn't select.
func acceptedValues[K comparable](ctx context.Context, m vlatte.Matcher, values []K) ([]K, error) {
	if m == nil {
		return nil, nil
	}

	var out []K

	for _, v := range values {
		ok, err := matchValue(ctx, m, v)
		if err != nil {
			return nil, err
		}

		if ok {
			out = append(out, v)
		}
	}

	return out, nil
}

// keeps tells whether row, a candidate, is one q selects by the fields rows hold that the index doesn't.
func (q *Query) keeps(ctx context.Context, row *journalRow) (bool, error) {
	if row.Seq <= q.After {
		return false, nil
	}

	if !q.Since.IsZero() && row.Time.Before(q.Since) {
		return false, nil
	}

	if !q.Until.IsZero() && !row.Time.Before(q.Until) {
		return false, nil
	}

	if q.Target != nil {
		return matchValue(ctx, q.Target, row.Target)
	}

	return true, nil
}

// readsEntries tells whether q, with filters, selects entries by what only their files hold.
func (q *Query) readsEntries(filters []Filter) bool {
	return len(q.Header) > 0 || q.Body != nil || len(filters) > 0
}

// selects tells whether q, with filters, selects e by what only its file holds.
func (q *Query) selects(ctx context.Context, e *Entry, filters []Filter) (bool, error) {
	for name, m := range q.Header {
		ok, err := matchValue(ctx, m, e.HeaderValue(name))
		if err != nil || !ok {
			return false, err
		}
	}

	// A body the journal cut can't tell whether the whole matched: the rest of the query goes first, as a miss there
	// is a miss.
	if q.Body != nil && !e.BodyTruncated {
		ok, err := matchValue(ctx, q.Body, string(e.Body))
		if err != nil || !ok {
			return false, err
		}
	}

	for _, filter := range filters {
		ok, err := filter(ctx, e)
		if err != nil || !ok {
			return false, err
		}
	}

	if q.Body != nil && e.BodyTruncated {
		return false, fmt.Errorf("journal: %w: entry %d: the journal kept part of its body, which the query reads",
			vlatte.ErrMatching, e.Seq)
	}

	return true, nil
}

func (q *Query) check() error {
	switch {
	case q.Limit < 0:
		return fmt.Errorf("journal: limit must be 0 or more, not %d", q.Limit)
	case q.Outcome != OutcomeUnknown && !q.Outcome.Valid():
		return fmt.Errorf("journal: outcome %d is not one of matched, unmatched, proxied, failed or abandoned", q.Outcome)
	}

	for name, m := range q.Header {
		if m == nil {
			return fmt.Errorf("journal: header %q has no matcher", name)
		}
	}

	return nil
}

// Valid tells whether o is one of the outcomes a journal knows.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeMatched, OutcomeUnmatched, OutcomeProxied, OutcomeFailed, OutcomeAbandoned:
		return true
	}

	return false
}

// matchValue runs m on v. A matcher that fails, or panics, fails the query, with ErrMatching or ErrMatchPanicked,
// instead of the server.
func matchValue(ctx context.Context, m vlatte.Matcher, v any) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("journal: %w: %T: %v", vlatte.ErrMatchPanicked, m, r)
		}
	}()

	ok, err = m.Match(vlatte.NewMatcherArg(ctx, vlatte.NewExplanation(), v))
	if err != nil {
		return false, fmt.Errorf("journal: %w: %s: %w", vlatte.ErrMatching, m.Name(), err)
	}

	return ok, nil
}

// searchRows returns where seq is in rows, ordered by sequence number, or would be, and whether it is.
func searchRows(rows []journalRow, seq uint64) (int, bool) {
	return slices.BinarySearchFunc(rows, seq, func(row journalRow, seq uint64) int { return cmp.Compare(row.Seq, seq) })
}

// intersect returns the numbers both a and b, each ordered, hold, in order.
func intersect(a, b []uint64) []uint64 {
	out := make([]uint64, 0, min(len(a), len(b)))

	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}

	return out
}

// union returns the numbers lists, each ordered and none sharing a number, hold, in order.
func union(lists [][]uint64) []uint64 {
	switch len(lists) {
	case 0:
		return nil
	case 1:
		return lists[0]
	}

	half := len(lists) / 2
	a, b := union(lists[:half]), union(lists[half:])
	out := make([]uint64, 0, len(a)+len(b))

	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] < b[j] {
			out = append(out, a[i])
			i++
		} else {
			out = append(out, b[j])
			j++
		}
	}

	out = append(out, a[i:]...)

	return append(out, b[j:]...)
}
