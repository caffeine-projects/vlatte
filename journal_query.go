package vlatte

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// EntryFilter tells whether an entry of a journal is one a query wants, such as a protocol's request pattern does.
type EntryFilter func(ctx context.Context, e *JournalEntry) (bool, error)

// JournalQuery selects entries of a [Journal]. Each field set narrows the entries it selects; the zero query selects
// them all. A matcher gets the value of its field: text, or an int for Status.
type JournalQuery struct {
	Kind    Kind
	Outcome Outcome

	MockID Matcher
	Method Matcher
	Path   Matcher
	Target Matcher
	Status Matcher

	// Since and Until bound the time of the request: Since includes it, Until doesn't.
	Since time.Time
	Until time.Time

	// Header matches headers by name, in any case: a matcher gets the first value of its header, or "" when the
	// request has none.
	Header map[string]Matcher

	// Body gets the body as text.
	Body Matcher

	// Limit is the most entries Find returns, the newest first; 0 returns them all. Count leaves it out.
	Limit int

	// Before selects the entries with a lower sequence number, those that follow a page Find returned, which its Next
	// names. Count leaves it out.
	Before uint64
}

// JournalPage is a page of the entries a query selects, the newest first. Count is the number of entries the query
// selects, its Limit and Before left out. Next, when not 0, is the Before of the next page.
type JournalPage struct {
	Entries []*JournalEntry
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
func (s *journalSnapshot) accept(ctx context.Context, q *JournalQuery) (*journalAccepted, error) {
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
func acceptedValues[K comparable](ctx context.Context, m Matcher, values []K) ([]K, error) {
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
func (q *JournalQuery) keeps(ctx context.Context, row *journalRow) (bool, error) {
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
func (q *JournalQuery) readsEntries(filters []EntryFilter) bool {
	return len(q.Header) > 0 || q.Body != nil || len(filters) > 0
}

// selects tells whether q, with filters, selects e by what only its file holds.
func (q *JournalQuery) selects(ctx context.Context, e *JournalEntry, filters []EntryFilter) (bool, error) {
	for name, m := range q.Header {
		ok, err := matchValue(ctx, m, firstValue(e.Header, name))
		if err != nil || !ok {
			return false, err
		}
	}

	if q.Body != nil {
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

	return true, nil
}

func (q *JournalQuery) check() error {
	switch {
	case q.Limit < 0:
		return fmt.Errorf("journal: limit must be 0 or more, not %d", q.Limit)
	case q.Outcome != "" && !q.Outcome.Valid():
		return fmt.Errorf("journal: outcome %q is not one of matched, unmatched, proxied, failed or abandoned", q.Outcome)
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

// matchValue runs m on v. A matcher that panics fails the query instead of the server.
func matchValue(ctx context.Context, m Matcher, v any) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("journal: %w: %T: %v", ErrMatchPanicked, m, r)
		}
	}()

	ok, err = m.Match(NewMatcherArg(ctx, NewExplanation(), v))
	if err != nil {
		return false, fmt.Errorf("journal: %s: %w", m.Name(), err)
	}

	return ok, nil
}

// firstValue returns the first value of the header of h named name, in any case, or "".
func firstValue(h map[string][]string, name string) string {
	if values := h[name]; len(values) > 0 {
		return values[0]
	}

	for k, values := range h {
		if strings.EqualFold(k, name) && len(values) > 0 {
			return values[0]
		}
	}

	return ""
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
