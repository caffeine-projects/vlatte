package vlatte_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/match"
)

var _ vlatte.RequestPatterns[*fakeMock, patterned] = patterned{}

// patterned is the server of a protocol with request patterns: a pattern names a path.
type patterned struct{}

func (patterned) RequestPattern(b vlatte.Builder[*fakeMock, patterned]) (vlatte.EntryFilter, error) {
	path, ok := b.(pathPattern)
	if !ok {
		return nil, errors.New("not a pattern")
	}

	return func(_ context.Context, e *vlatte.JournalEntry) (bool, error) { return e.Path == string(path), nil }, nil
}

// pathPattern is a pattern of patterned: the request part of a mock, here its path.
type pathPattern string

func (pathPattern) Build(patterned) (*fakeMock, error) {
	return nil, errors.New("a pattern builds no mock")
}

func journaled(t *testing.T, entries ...*vlatte.JournalEntry) *vlatte.Journal {
	t.Helper()

	j := newJournal(t, vlatte.JournalOptions{})
	for _, e := range entries {
		require.NoError(t, j.Append(e))
	}

	return j
}

func TestBase_AssertRequested(t *testing.T) {
	// A test verifies the requests a server got, as WireMock's verify does: the count is exact, a request no mock
	// matched counts as any other, and a failure lists what the server got, the newest first, with the mock that
	// came closest to one no mock matched.
	j := journaled(t,
		entry("a"),
		entry("b", func(e *vlatte.JournalEntry) {
			e.Outcome, e.Status = vlatte.OutcomeUnmatched, 418
			e.NearMisses = []vlatte.NearMiss{{MockID: "m1", Passed: 1, Total: 2, Failed: []string{"header Accept"}}}
		}),
	)
	app := vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock](), j)

	ft := testmock.NewFakeT()
	require.True(t, app.AssertRequested(ft, 2, vlatte.JournalQuery{Method: match.Equal("GET")}))
	require.True(t, app.AssertRequested(ft, 1, vlatte.JournalQuery{Outcome: vlatte.OutcomeUnmatched}))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)

	ft = testmock.NewFakeT()
	require.False(t, app.AssertRequested(ft, 1, vlatte.JournalQuery{Path: match.HasPrefix("/")}))

	report := errorText(t, ft)
	require.Contains(t, report, "Expected 1 request(s) matching the query.\nGot 2.")
	require.Contains(t, report, "Requests matching the query (2 of 2, the newest first):")
	require.Contains(t, report, "#2 2026-10-21T12:00:00.000Z unmatched GET http://127.0.0.1/b 418, closest [m1] differs in header Accept\n    #1")
	require.Contains(t, report, "matched GET http://127.0.0.1/a 200")

	// None matched: the requests the server served instead tell why.
	ft = testmock.NewFakeT()
	require.False(t, app.AssertRequested(ft, 1, vlatte.JournalQuery{Path: match.Equal("/c")}))

	report = errorText(t, ft)
	require.Contains(t, report, "Got 0.")
	require.Contains(t, report, "Requests the server served (2 of 2, the newest first):")
}

func TestBase_AssertsInACleanup(t *testing.T) {
	// A test checks its requests once it ends, in a cleanup, when its context is done: the assertion still runs.
	app := vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock](), journaled(t, entry("a")))

	t.Run("cleanup", func(t *testing.T) {
		t.Cleanup(func() {
			require.Error(t, t.Context().Err())
			require.True(t, app.AssertRequested(t, 1, vlatte.JournalQuery{Path: match.Equal("/a")}))
		})
	})
}

func TestBase_AssertNotRequested(t *testing.T) {
	// The failure lists the requests that should not have come.
	j := journaled(t, entry("a"), entry("b", func(e *vlatte.JournalEntry) { e.Method = "POST" }))
	app := vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock](), j)

	ft := testmock.NewFakeT()
	require.True(t, app.AssertNotRequested(ft, vlatte.JournalQuery{Method: match.Equal("PUT")}))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)

	ft = testmock.NewFakeT()
	require.False(t, app.AssertNotRequested(ft, vlatte.JournalQuery{Method: match.Equal("POST")}))

	report := errorText(t, ft)
	require.Contains(t, report, "1 request(s) matched the query when none should.")
	require.Contains(t, report, "POST http://127.0.0.1/b 200")
	require.NotContains(t, report, "/a")
}

func TestBase_VerifiesWithTheRequestPatternsOfItsProtocol(t *testing.T) {
	// A protocol verifies with the request part of its own mocks, as a mock matches: its patterns narrow what the
	// query selects.
	j := journaled(t, entry("a"), entry("b"), entry("c", func(e *vlatte.JournalEntry) { e.Method = "POST" }))
	app := vlatte.NewBase[*fakeMock](patterned{}, vlatte.NewStore[*fakeMock](), j)

	got, err := app.Requests(t.Context(), vlatte.JournalQuery{Method: match.Equal("GET")}, pathPattern("/b"))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].ID)

	count, err := app.CountRequests(t.Context(), vlatte.JournalQuery{}, pathPattern("/c"), pathPattern("/b"))
	require.NoError(t, err)
	require.Zero(t, count, "every pattern must match")

	ft := testmock.NewFakeT()
	require.True(t, app.AssertRequested(ft, 1, vlatte.JournalQuery{}, pathPattern("/c")))
	require.True(t, app.AssertNotRequested(ft, vlatte.JournalQuery{Method: match.Equal("GET")}, pathPattern("/c")))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestBase_VerificationFailsLoudly(t *testing.T) {
	// A server without a journal, or whose protocol has no request patterns, can't verify: an assertion fails with
	// the reason, never passes for want of requests.
	tests := []struct {
		name  string
		app   *vlatte.Base[*fakeMock, struct{}]
		give  []vlatte.Builder[*fakeMock, struct{}]
		wants error
	}{
		{
			name:  "no journal",
			app:   vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock](), nil),
			wants: vlatte.ErrNoJournal,
		},
		{
			name:  "no request patterns",
			app:   vlatte.NewBase[*fakeMock](struct{}{}, vlatte.NewStore[*fakeMock](), journaled(t)),
			give:  []vlatte.Builder[*fakeMock, struct{}]{fakeBuilder{}},
			wants: vlatte.ErrNoRequestPatterns,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.app.CountRequests(t.Context(), vlatte.JournalQuery{}, tt.give...)
			require.ErrorIs(t, err, tt.wants)

			_, err = tt.app.Requests(t.Context(), vlatte.JournalQuery{}, tt.give...)
			require.ErrorIs(t, err, tt.wants)

			ft := testmock.NewFakeT()
			require.False(t, tt.app.AssertRequested(ft, 0, vlatte.JournalQuery{}, tt.give...))
			require.Contains(t, errorText(t, ft), tt.wants.Error())

			ft = testmock.NewFakeT()
			require.False(t, tt.app.AssertNotRequested(ft, vlatte.JournalQuery{}, tt.give...))
			require.Contains(t, errorText(t, ft), tt.wants.Error())
		})
	}
}
