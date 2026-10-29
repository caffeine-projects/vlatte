package verify

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
)

// report lists in r the requests that tell why s failed with r's count, and says it in r's Report: those s selected,
// and, when s wanted more, those of its window that came closest, with the parts they missed.
func (s *Spec) report(ctx context.Context, filters []journal.Filter, r *Result) error {
	selected, err := s.Journal.Find(ctx, s.withLimit(_listed), filters...)
	if err != nil {
		return err
	}

	for _, e := range selected.Entries {
		r.Selected = append(r.Selected, Listed{Seq: e.Seq, ID: e.ID, Line: describeEntry(e)})
	}

	wantsMore, err := s.wantsMore(ctx, r.Count)
	if err != nil {
		return err
	}

	window, err := s.Journal.Find(ctx, journal.Query{After: s.Query.After, Limit: _scanned})
	if err != nil {
		return err
	}

	if wantsMore && len(window.Entries) > 0 {
		// Every request of the window s selects, not only those listed, came in, not close.
		in := s.Query
		in.After, in.Before = window.Entries[len(window.Entries)-1].Seq-1, window.Entries[0].Seq+1

		page, err := s.Journal.Find(ctx, in, filters...)
		if err != nil {
			return err
		}

		if r.Closest, err = s.closest(ctx, window.Entries, page.Entries); err != nil {
			return err
		}
	}

	var b strings.Builder

	fmt.Fprintf(&b, "\nExpected %s request(s)%s%s.\nGot %d", s.Expect, s.describe(), s.describeWait(), r.Count)

	if wait := max(s.Within, s.During); wait > 0 {
		fmt.Fprintf(&b, " after %s", r.Waited.Round(time.Millisecond))
	}

	b.WriteString(".\n")

	where := "the server served"
	if s.Marked {
		where = fmt.Sprintf("since mark #%d", s.Query.After)
	}

	if window.Count == 0 {
		fmt.Fprintf(&b, "  Requests %s: none\n", where)
	}

	if len(r.Selected) > 0 {
		fmt.Fprintf(&b, "  Requests selected (%d of %d, the newest first):\n", len(r.Selected), r.Count)
		writeListed(&b, r.Selected)
	}

	if len(r.Closest) > 0 {
		fmt.Fprintf(&b, "  Closest requests (%d of %d %s, the newest first):\n", len(r.Closest), window.Count, where)
		writeListed(&b, r.Closest)
	}

	r.Report = b.String()

	return nil
}

// withLimit returns the query of s with a limit of n, to list what it selects.
func (s *Spec) withLimit(n int) journal.Query {
	q := s.Query
	q.Limit = n

	return q
}

// wantsMore tells whether a count above count passes s, so that more requests were wanted than s selected.
func (s *Spec) wantsMore(ctx context.Context, count int) (bool, error) {
	for n := count + 1; n <= count+_wantsMore; n++ {
		passed, err := countPasses(ctx, s.Count, n)
		if err != nil || passed {
			return passed, err
		}
	}

	return false, nil
}

// closest ranks the entries of window that s didn't select, those of selected, by the parts of s they missed, and
// returns the closest: those that missed the fewest, the newest first among those that missed as many.
func (s *Spec) closest(ctx context.Context, window, selected []*journal.Entry) ([]Listed, error) {
	parts := s.parts()

	var ranked []Listed

	for _, e := range window {
		if slices.ContainsFunc(selected, func(sel *journal.Entry) bool { return sel.Seq == e.Seq }) {
			continue
		}

		miss, err := s.explain(ctx, parts, e)
		if err != nil {
			return nil, err
		}

		ranked = append(ranked, Listed{
			Seq: e.Seq, ID: e.ID, Line: describeEntry(e), Passed: miss.Passed, Total: miss.Total, Missed: miss.Failed,
		})
	}

	// The window is the newest first: a stable sort keeps the newest first among those that missed as many.
	slices.SortStableFunc(ranked, func(a, b Listed) int { return cmp.Compare(len(a.Missed), len(b.Missed)) })

	return ranked[:min(len(ranked), _listed)], nil
}

// explain tells which of parts, the criteria of s, and of s's patterns, e passes and which it fails.
func (s *Spec) explain(ctx context.Context, parts []*vlatte.Eval[*journal.Entry], e *journal.Entry) (Miss, error) {
	var miss Miss

	for _, part := range parts {
		// A body the journal cut can't tell whether the whole matched.
		if part.Key == "body" && e.BodyTruncated {
			continue
		}

		miss.Total++

		if ok, err := part.Match(ctx, nil, e); err == nil && ok {
			miss.Passed++
		} else {
			miss.Failed = append(miss.Failed, part.Key)
		}
	}

	for _, p := range s.Patterns {
		m, err := p.Explain(ctx, e)
		if err != nil {
			return Miss{}, err
		}

		miss.Passed += m.Passed
		miss.Total += m.Total
		miss.Failed = append(miss.Failed, m.Failed...)
	}

	return miss, nil
}

// parts returns the criteria of s as evaluators of an entry, each named after the part of a request it checks.
func (s *Spec) parts() []*vlatte.Eval[*journal.Entry] {
	var parts []*vlatte.Eval[*journal.Entry]

	add := func(name string, m vlatte.Matcher, sel func(e *journal.Entry) any) {
		if m != nil {
			parts = append(parts, &vlatte.Eval[*journal.Entry]{
				Key: name, Matcher: m, Selector: func(_ context.Context, e *journal.Entry) any { return sel(e) },
			})
		}
	}

	q := &s.Query

	if q.Kind != "" {
		add("kind", vlatte.MatcherFunc("kind", func(arg vlatte.MatcherArg) (bool, error) {
			return arg.Value() == q.Kind, nil
		}), func(e *journal.Entry) any { return e.Kind })
	}

	if outcomes := s.outcomes(); len(outcomes) > 0 {
		add("outcome", vlatte.MatcherFunc("outcome", func(arg vlatte.MatcherArg) (bool, error) {
			o, _ := arg.Value().(journal.Outcome)
			return slices.Contains(outcomes, o), nil
		}), func(e *journal.Entry) any { return e.Outcome })
	}

	add("mock id", q.MockID, func(e *journal.Entry) any { return e.MockID })
	add("method", q.Method, func(e *journal.Entry) any { return e.Method })
	add("path", q.Path, func(e *journal.Entry) any { return e.Path })
	add("target", q.Target, func(e *journal.Entry) any { return e.Target })
	add("status", q.Status, func(e *journal.Entry) any { return e.Status })

	for _, name := range slices.Sorted(maps.Keys(q.Header)) {
		add("header "+name, q.Header[name], func(e *journal.Entry) any { return e.HeaderValue(name) })
	}

	add("body", q.Body, func(e *journal.Entry) any { return string(e.Body) })

	return parts
}

// outcomes returns the outcomes s selects by.
func (s *Spec) outcomes() []journal.Outcome {
	if len(s.Outcomes) == 0 && s.Query.Outcome != journal.OutcomeUnknown {
		return []journal.Outcome{s.Query.Outcome}
	}

	return s.Outcomes
}

// describe names what s selects by, such as " with method, path, header Accept and a pattern".
func (s *Spec) describe() string {
	var names []string
	for _, part := range s.parts() {
		names = append(names, part.Key)
	}

	switch len(s.Patterns) {
	case 0:
	case 1:
		names = append(names, "a pattern")
	default:
		names = append(names, fmt.Sprintf("%d patterns", len(s.Patterns)))
	}

	if s.Query.Since != (time.Time{}) || s.Query.Until != (time.Time{}) {
		names = append(names, "time")
	}

	if len(names) == 0 {
		return ""
	}

	return " with " + strings.Join(names, ", ")
}

// describeWait says how s waits.
func (s *Spec) describeWait() string {
	switch {
	case s.Within > 0:
		return " within " + s.Within.String()
	case s.During > 0:
		return " for " + s.During.String()
	}

	return ""
}

// writeListed writes requests to b, a line each, and the parts each missed, if any, on the next.
func writeListed(b *strings.Builder, requests []Listed) {
	for _, l := range requests {
		b.WriteString("    ")
		b.WriteString(l.Line)
		b.WriteString("\n")

		if len(l.Missed) > 0 {
			fmt.Fprintf(b, "        missed %d of %d: %s\n", len(l.Missed), l.Total, strings.Join(l.Missed, ", "))
		}
	}
}

// describeEntry describes e in a line: its number, time, outcome, method, target and status, the mock that matched
// it, or the closest one that didn't, and what failed, the lines of its message joined with "; ". It leaves out its
// headers and body, which may hold secrets.
func describeEntry(e *journal.Entry) string {
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
		b.WriteString(strings.Join(e.Messages(), "; "))
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
