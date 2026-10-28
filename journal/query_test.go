package journal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
)

func TestJournal_AMatcherThatFailsFailsTheQuery(t *testing.T) {
	// A user's matcher runs in a test or an admin request: its error fails the query, on any field, which the admin
	// API answers as the query's problem, rather than leave out the entries it couldn't check.
	j := newJournal(t, journal.Options{})
	require.NoError(t, j.Append(entry("a", func(e *journal.Entry) {
		e.MockID, e.Header, e.Body = "m1", map[string][]string{"Accept": {"*/*"}}, []byte("hi")
	})))

	errBad := errors.New("bad")
	bad := vlatte.MatcherFunc("bad", func(vlatte.MatcherArg) (bool, error) { return false, errBad })

	tests := []struct {
		field string
		give  journal.Query
	}{
		{field: "mock id", give: journal.Query{MockID: bad}},
		{field: "method", give: journal.Query{Method: bad}},
		{field: "path", give: journal.Query{Path: bad}},
		{field: "status", give: journal.Query{Status: bad}},
		{field: "target", give: journal.Query{Target: bad}},
		{field: "header", give: journal.Query{Header: map[string]vlatte.Matcher{"Accept": bad}}},
		{field: "body", give: journal.Query{Body: bad}},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			_, err := j.Find(t.Context(), tt.give)
			require.ErrorIs(t, err, vlatte.ErrMatching)
			require.ErrorIs(t, err, errBad)
		})
	}
}

func TestJournal_AHeaderNamedAsAskedComesFirst(t *testing.T) {
	// A header is found in any case, but an entry may keep two spellings of one name, as a server given its headers
	// as they came does: the one named as the query names it is the one that counts, whichever the other is.
	j := newJournal(t, journal.Options{})

	// The spellings are tried in no set order: entries enough that a lookup that tried them so fails at least once.
	for range 16 {
		require.NoError(t, j.Append(entry("a", func(e *journal.Entry) {
			e.Header = map[string][]string{"X-Team": {"core"}, "x-team": {"edge"}}
		})))
	}

	for name, value := range map[string]string{"X-Team": "core", "x-team": "edge"} {
		count, err := j.Count(t.Context(), journal.Query{Header: map[string]vlatte.Matcher{name: match.Equal(value)}})
		require.NoError(t, err)
		require.Equal(t, 16, count, name)
	}
}

func TestJournal_AfterSelectsWhatCameSinceAPointInTheJournal(t *testing.T) {
	// A test that shares a server counts the requests it sent, and none of those before: it takes the newest sequence
	// number before it sends them, and selects the entries after it. Count selects by it too, unlike Before, so a
	// count is of the same entries Find returns. A Clear between keeps it right, as numbers go on.
	j := newJournal(t, journal.Options{})
	require.NoError(t, j.Append(entry("a")))
	require.NoError(t, j.Append(entry("b")))

	mark := j.Last()
	require.Equal(t, uint64(2), mark)

	require.NoError(t, j.Append(entry("c")))
	require.NoError(t, j.Append(entry("d", func(e *journal.Entry) { e.Method = "POST" })))

	require.Equal(t, []string{"d", "c"}, find(t, j, journal.Query{After: mark}))
	require.Equal(t, []string{"c"}, find(t, j, journal.Query{After: mark, Method: match.Equal("GET")}))
	require.Empty(t, find(t, j, journal.Query{After: j.Last()}))

	mark = j.Last()
	require.NoError(t, j.Clear())
	require.NoError(t, j.Append(entry("e")))
	require.Equal(t, []string{"e"}, find(t, j, journal.Query{After: mark}))
}

func TestJournal_ABodyQueryCantTellOfABodyTheJournalCut(t *testing.T) {
	// The journal keeps MaxBodySize bytes of a body. A query of the body matched the part kept as if it were the
	// whole, so a request whose cut-off part held what the query looks for counted as a miss, and a verification that
	// none came passed. The query fails instead, once the rest of it matches; a query the rest rules out still counts.
	j := newJournal(t, journal.Options{MaxBodySize: 4})
	require.NoError(t, j.Append(entry("a", func(e *journal.Entry) { e.Body = []byte("abcd-and-the-rest") })))

	_, err := j.Count(t.Context(), journal.Query{Body: match.HasSuffix("rest")})
	require.ErrorIs(t, err, vlatte.ErrMatching)
	require.ErrorContains(t, err, "entry 1: the journal kept part of its body")

	count, err := j.Count(t.Context(), journal.Query{Body: match.HasSuffix("rest"), Header: map[string]vlatte.Matcher{"X-Other": match.Equal("1")}})
	require.NoError(t, err)
	require.Zero(t, count, "the header rules it out: what the body holds doesn't count")

	_, err = j.Find(t.Context(), journal.Query{Body: match.HasSuffix("rest")}, func(context.Context, *journal.Entry) (bool, error) { return false, nil })
	require.NoError(t, err, "a filter rules it out")
}
