package vlatte_test

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// modelQuery is a query of the journal, with the plain filter the model runs for it.
type modelQuery struct {
	q    vlatte.JournalQuery
	keep func(e *vlatte.JournalEntry) bool
}

func TestJournalMachine(t *testing.T) {
	// The index is how the journal avoids reading every entry: whatever mix of appends, queries, clears, restarts
	// and dropped entries happens, a query through it must select exactly the entries a plain scan of every entry
	// selects, in the same order, with the same count and next page.
	base := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(base, "")
		require.NoError(t, err)

		opts := vlatte.JournalOptions{Dir: dir, MaxEntries: rapid.IntRange(0, 12).Draw(t, "max")}

		j, err := vlatte.NewJournal(opts)
		require.NoError(t, err)

		defer func() { j.Close() }()

		var (
			model []*vlatte.JournalEntry
			next  int
		)

		t.Repeat(map[string]func(*rapid.T){
			"append": func(t *rapid.T) {
				e := drawEntry(t, strconv.Itoa(next))
				next++

				require.NoError(t, j.Append(e))

				model = append(model, e)
				if opts.MaxEntries > 0 && len(model) > opts.MaxEntries {
					model = model[len(model)-opts.MaxEntries:]
				}
			},
			"find": func(t *rapid.T) {
				mq := drawQuery(t, model)

				page, err := j.Find(context.Background(), mq.q)
				require.NoError(t, err)

				count, err := j.Count(context.Background(), mq.q)
				require.NoError(t, err)

				want := scan(model, mq)
				require.Equal(t, want.Count, count, "count")
				require.Equal(t, want, page)
			},
			"get": func(t *rapid.T) {
				id := strconv.Itoa(rapid.IntRange(0, next).Draw(t, "id"))

				got, err := j.Get(id)

				i := slices.IndexFunc(model, func(e *vlatte.JournalEntry) bool { return e.ID == id })
				if i < 0 {
					require.ErrorIs(t, err, vlatte.ErrRequestNotFound)
					return
				}

				require.NoError(t, err)
				require.Equal(t, model[i], got)
			},
			"clear": func(t *rapid.T) {
				require.NoError(t, j.Clear())
				model = nil
			},
			"restart": func(t *rapid.T) {
				require.NoError(t, j.Close())

				j, err = vlatte.NewJournal(opts)
				require.NoError(t, err)
			},
		})
	})
}

// scan selects the entries of model that mq keeps, as Find does, without an index.
func scan(model []*vlatte.JournalEntry, mq modelQuery) vlatte.JournalPage {
	var page vlatte.JournalPage

	for _, e := range slices.Backward(model) {
		if !mq.keep(e) {
			continue
		}

		page.Count++

		if mq.q.Before != 0 && e.Seq >= mq.q.Before {
			continue
		}

		if mq.q.Limit > 0 && len(page.Entries) == mq.q.Limit {
			if page.Next == 0 {
				page.Next = page.Entries[len(page.Entries)-1].Seq
			}

			continue
		}

		page.Entries = append(page.Entries, e)
	}

	return page
}

func drawEntry(t *rapid.T, id string) *vlatte.JournalEntry {
	path := rapid.SampledFrom([]string{"/a", "/b", "/c/d"}).Draw(t, "path")

	e := &vlatte.JournalEntry{
		ID:      id,
		Time:    _t0.Add(time.Duration(rapid.IntRange(0, 10).Draw(t, "minute")) * time.Minute),
		Kind:    rapid.SampledFrom([]vlatte.Kind{"http", "grpc"}).Draw(t, "kind"),
		Outcome: rapid.SampledFrom(_outcomes).Draw(t, "outcome"),
		MockID:  rapid.SampledFrom([]string{"", "m1", "m2"}).Draw(t, "mock"),
		Method:  rapid.SampledFrom([]string{"", "GET", "POST"}).Draw(t, "method"),
		Path:    path,
		Target:  "http://h" + path + rapid.SampledFrom([]string{"", "?q=1"}).Draw(t, "query"),
		Status:  rapid.SampledFrom([]int{0, 200, 404}).Draw(t, "status"),
		Elapsed: time.Duration(rapid.IntRange(0, 1000).Draw(t, "elapsed")),
	}

	if v := rapid.SampledFrom([]string{"", "1", "2"}).Draw(t, "header"); v != "" {
		e.Header = map[string][]string{rapid.SampledFrom([]string{"X-A", "x-a"}).Draw(t, "name"): {v}}
	}

	if body := rapid.SampledFrom([]string{"", "x", `{"id":1}`, "\xff\x00"}).Draw(t, "body"); body != "" {
		e.Body = []byte(body)
	}

	return e
}

var _outcomes = []vlatte.Outcome{
	vlatte.OutcomeMatched,
	vlatte.OutcomeUnmatched,
	vlatte.OutcomeProxied,
	vlatte.OutcomeFailed,
	vlatte.OutcomeAbandoned,
}

// drawQuery draws a query of a few fields, each with the plain check the model runs for it.
func drawQuery(t *rapid.T, model []*vlatte.JournalEntry) modelQuery {
	var (
		q      vlatte.JournalQuery
		checks []func(e *vlatte.JournalEntry) bool
	)

	text := func(label string, values []string, set func(m vlatte.Matcher), get func(e *vlatte.JournalEntry) string) {
		if !rapid.Bool().Draw(t, label) {
			return
		}

		picked := rapid.SliceOfNDistinct(rapid.SampledFrom(values), 1, 2, rapid.ID[string]).Draw(t, label+" values")

		matchers := make([]vlatte.Matcher, len(picked))
		for i, v := range picked {
			matchers[i] = match.Equal(v)
		}

		set(match.Any(matchers...))
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return slices.Contains(picked, get(e)) })
	}

	if rapid.Bool().Draw(t, "by kind") {
		q.Kind = rapid.SampledFrom([]vlatte.Kind{"http", "grpc", "ws"}).Draw(t, "kind")
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return e.Kind == q.Kind })
	}

	if rapid.Bool().Draw(t, "by outcome") {
		q.Outcome = rapid.SampledFrom(_outcomes).Draw(t, "outcome")
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return e.Outcome == q.Outcome })
	}

	text("by mock", []string{"", "m1", "m2", "m3"}, func(m vlatte.Matcher) { q.MockID = m }, func(e *vlatte.JournalEntry) string { return e.MockID })
	text("by method", []string{"", "GET", "POST"}, func(m vlatte.Matcher) { q.Method = m }, func(e *vlatte.JournalEntry) string { return e.Method })
	text("by path", []string{"/a", "/b", "/c/d"}, func(m vlatte.Matcher) { q.Path = m }, func(e *vlatte.JournalEntry) string { return e.Path })

	if rapid.Bool().Draw(t, "by target") {
		q.Target = match.Contain("?q=1")
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return strings.Contains(e.Target, "?q=1") })
	}

	if rapid.Bool().Draw(t, "by status") {
		least := rapid.SampledFrom([]int{0, 200, 404}).Draw(t, "least status")
		q.Status = match.GreaterThanOrEqual(least)
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return e.Status >= least })
	}

	if rapid.Bool().Draw(t, "by time") {
		since := _t0.Add(time.Duration(rapid.IntRange(0, 10).Draw(t, "since")) * time.Minute)
		until := since.Add(time.Duration(rapid.IntRange(0, 10).Draw(t, "span")) * time.Minute)
		q.Since, q.Until = since, until
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return !e.Time.Before(since) && e.Time.Before(until) })
	}

	if rapid.Bool().Draw(t, "by header") {
		v := rapid.SampledFrom([]string{"", "1", "2"}).Draw(t, "header value")
		q.Header = map[string]vlatte.Matcher{"x-A": match.Equal(v)}
		checks = append(checks, func(e *vlatte.JournalEntry) bool {
			got := ""
			for k, values := range e.Header {
				if strings.EqualFold(k, "x-A") {
					got = values[0]
				}
			}

			return got == v
		})
	}

	if rapid.Bool().Draw(t, "by body") {
		v := rapid.SampledFrom([]string{"", "x", `{"id":1}`}).Draw(t, "body value")
		q.Body = match.Equal(v)
		checks = append(checks, func(e *vlatte.JournalEntry) bool { return string(e.Body) == v })
	}

	q.Limit = rapid.IntRange(0, 4).Draw(t, "limit")

	if len(model) > 0 && rapid.Bool().Draw(t, "paged") {
		q.Before = rapid.SampledFrom(model).Draw(t, "before").Seq
	}

	return modelQuery{q: q, keep: func(e *vlatte.JournalEntry) bool {
		for _, check := range checks {
			if !check(e) {
				return false
			}
		}

		return true
	}}
}
