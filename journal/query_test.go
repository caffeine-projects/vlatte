package journal_test

import (
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
