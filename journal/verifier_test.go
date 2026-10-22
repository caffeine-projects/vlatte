package journal_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
)

var _ journal.RequestPatterns[*fakeMock, patterned] = patterned{}

// fakeMock is a mock of no protocol. No test here builds one: a pattern is a builder, whose mock counts for nothing.
type fakeMock struct{}

func (*fakeMock) ID() string            { return "" }
func (*fakeMock) Name() string          { return "" }
func (*fakeMock) Priority() int         { return 0 }
func (*fakeMock) Hits() int64           { return 0 }
func (*fakeMock) Called() bool          { return false }
func (*fakeMock) Enabled() bool         { return true }
func (*fakeMock) Enable()               {}
func (*fakeMock) Disable()              {}
func (*fakeMock) Events() vlatte.Events { return vlatte.Events{} }
func (*fakeMock) String() string        { return "fake" }

// fakeBuilder is a builder of a protocol without request patterns.
type fakeBuilder struct{}

func (fakeBuilder) Build(struct{}) (*fakeMock, error) { return &fakeMock{}, nil }

// patterned is the server of a protocol with request patterns: a pattern names a path.
type patterned struct{}

func (patterned) RequestPattern(b vlatte.Builder[*fakeMock, patterned]) (journal.Filter, error) {
	path, ok := b.(pathPattern)
	if !ok {
		return nil, errors.New("not a pattern")
	}

	return func(_ context.Context, e *journal.Entry) (bool, error) { return e.Path == string(path), nil }, nil
}

// pathPattern is a pattern of patterned: the request part of a mock, here its path.
type pathPattern string

func (pathPattern) Build(patterned) (*fakeMock, error) {
	return nil, errors.New("a pattern builds no mock")
}

func journaled(t *testing.T, entries ...*journal.Entry) *journal.Journal {
	t.Helper()

	j := newJournal(t, journal.Options{})
	for _, e := range entries {
		require.NoError(t, j.Append(e))
	}

	return j
}

// errorText returns the message of the one Errorf call ft received.
func errorText(t *testing.T, ft *testmock.FakeT) string {
	t.Helper()

	var calls []mock.Call
	for _, call := range ft.Calls {
		if call.Method == "Errorf" {
			calls = append(calls, call)
		}
	}

	require.Len(t, calls, 1)

	args, _ := calls[0].Arguments.Get(1).([]any)

	return fmt.Sprintf(calls[0].Arguments.String(0), args...)
}

func TestVerifier_AssertRequested(t *testing.T) {
	// A test verifies the requests a server got, as WireMock's verify does: the count is exact, a request no mock
	// matched counts as any other, and a failure lists what the server got, the newest first, with the mock that
	// came closest to one no mock matched.
	j := journaled(t,
		entry("a"),
		entry("b", func(e *journal.Entry) {
			e.Outcome, e.Status = journal.OutcomeUnmatched, 418
			e.NearMisses = []vlatte.NearMiss{{MockID: "m1", Passed: 1, Total: 2, Failed: []string{"header Accept"}}}
		}),
	)
	app := journal.NewVerifier[*fakeMock, struct{}](j, nil)

	ft := testmock.NewFakeT()
	require.True(t, app.AssertRequested(ft, 2, journal.Query{Method: match.Equal("GET")}))
	require.True(t, app.AssertRequested(ft, 1, journal.Query{Outcome: journal.OutcomeUnmatched}))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)

	ft = testmock.NewFakeT()
	require.False(t, app.AssertRequested(ft, 1, journal.Query{Path: match.HasPrefix("/")}))

	report := errorText(t, ft)
	require.Contains(t, report, "Expected 1 request(s) matching the query.\nGot 2.")
	require.Contains(t, report, "Requests matching the query (2 of 2, the newest first):")
	require.Contains(t, report, "#2 2026-10-21T12:00:00.000Z unmatched GET http://127.0.0.1/b 418, closest [m1] differs in header Accept\n    #1")
	require.Contains(t, report, "matched GET http://127.0.0.1/a 200")

	// None matched: the requests the server served instead tell why.
	ft = testmock.NewFakeT()
	require.False(t, app.AssertRequested(ft, 1, journal.Query{Path: match.Equal("/c")}))

	report = errorText(t, ft)
	require.Contains(t, report, "Got 0.")
	require.Contains(t, report, "Requests the server served (2 of 2, the newest first):")
}

func TestVerifier_AFailureTellsWhatCameOfEachRequest(t *testing.T) {
	// A failure's list says, of each request, what a test needs to tell why it was or wasn't the one expected: the
	// mock that answered it, a body the journal cut, what failed, and the mock that came closest, which only a part
	// that keeps state, such as Times, kept from matching.
	j := journaled(t,
		entry("a", func(e *journal.Entry) { e.MockID, e.BodyTruncated = "m1", true }),
		entry("b", func(e *journal.Entry) { e.Outcome, e.Status, e.Message = journal.OutcomeFailed, 500, "reply: boom" }),
		entry("c", func(e *journal.Entry) {
			e.Outcome, e.Status = journal.OutcomeUnmatched, 418
			e.NearMisses = []vlatte.NearMiss{{MockID: "m2", Passed: 2, Total: 2, Skipped: []string{"times"}}}
		}),
	)
	app := journal.NewVerifier[*fakeMock, struct{}](j, nil)

	ft := testmock.NewFakeT()
	require.False(t, app.AssertNotRequested(ft, journal.Query{}))

	report := errorText(t, ft)
	require.Contains(t, report, "unmatched GET http://127.0.0.1/c 418, closest [m2] missed by times\n")
	require.Contains(t, report, "failed GET http://127.0.0.1/b 500: reply: boom\n")
	require.Contains(t, report, "matched GET http://127.0.0.1/a 200 [m1] (body cut)\n")
}

func TestVerifier_AFailureSaysWhenTheServerServedNothing(t *testing.T) {
	app := journal.NewVerifier[*fakeMock, struct{}](journaled(t), nil)

	ft := testmock.NewFakeT()
	require.False(t, app.AssertRequested(ft, 1, journal.Query{}))
	require.Contains(t, errorText(t, ft), "Got 0.\n  Requests the server served: none\n")
}

func TestVerifier_AFailureSaysWhenItCantListTheRequests(t *testing.T) {
	// None matched, and the requests the server served can't be read: the failure says so, with why, rather than
	// list none, as if the server had served nothing.
	dir := t.TempDir()
	j := newJournal(t, journal.Options{Dir: dir})
	require.NoError(t, j.Append(entry("a")))
	require.NoError(t, os.Truncate(filepath.Join(dir, "journal-000001.log"), 0))

	app := journal.NewVerifier[*fakeMock, struct{}](j, nil)

	ft := testmock.NewFakeT()
	require.False(t, app.AssertRequested(ft, 1, journal.Query{Path: match.Equal("/b")}))

	report := errorText(t, ft)
	require.Contains(t, report, "Got 0.\nCould not list the requests.\n")
	require.Contains(t, report, io.EOF.Error())
}

func TestVerifier_AssertsInACleanup(t *testing.T) {
	// A test checks its requests once it ends, in a cleanup, when its context is done: the assertion still runs.
	app := journal.NewVerifier[*fakeMock, struct{}](journaled(t, entry("a")), nil)

	t.Run("cleanup", func(t *testing.T) {
		t.Cleanup(func() {
			require.Error(t, t.Context().Err())
			require.True(t, app.AssertRequested(t, 1, journal.Query{Path: match.Equal("/a")}))
		})
	})
}

func TestVerifier_AssertNotRequested(t *testing.T) {
	// The failure lists the requests that should not have come.
	j := journaled(t, entry("a"), entry("b", func(e *journal.Entry) { e.Method = "POST" }))
	app := journal.NewVerifier[*fakeMock, struct{}](j, nil)

	ft := testmock.NewFakeT()
	require.True(t, app.AssertNotRequested(ft, journal.Query{Method: match.Equal("PUT")}))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)

	ft = testmock.NewFakeT()
	require.False(t, app.AssertNotRequested(ft, journal.Query{Method: match.Equal("POST")}))

	report := errorText(t, ft)
	require.Contains(t, report, "1 request(s) matched the query when none should.")
	require.Contains(t, report, "POST http://127.0.0.1/b 200")
	require.NotContains(t, report, "/a")
}

func TestVerifier_VerifiesWithTheRequestPatternsOfItsProtocol(t *testing.T) {
	// A protocol verifies with the request part of its own mocks, as a mock matches: its patterns narrow what the
	// query selects.
	j := journaled(t, entry("a"), entry("b"), entry("c", func(e *journal.Entry) { e.Method = "POST" }))
	app := journal.NewVerifier[*fakeMock](j, patterned{})

	got, err := app.Requests(t.Context(), journal.Query{Method: match.Equal("GET")}, pathPattern("/b"))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].ID)

	count, err := app.CountRequests(t.Context(), journal.Query{}, pathPattern("/c"), pathPattern("/b"))
	require.NoError(t, err)
	require.Zero(t, count, "every pattern must match")

	ft := testmock.NewFakeT()
	require.True(t, app.AssertRequested(ft, 1, journal.Query{}, pathPattern("/c")))
	require.True(t, app.AssertNotRequested(ft, journal.Query{Method: match.Equal("GET")}, pathPattern("/c")))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestVerifier_VerificationFailsLoudly(t *testing.T) {
	// A server without a journal, or whose protocol has no request patterns, can't verify: an assertion fails with
	// the reason, never passes for want of requests.
	tests := []struct {
		name  string
		app   *journal.Verifier[*fakeMock, struct{}]
		give  []vlatte.Builder[*fakeMock, struct{}]
		wants error
	}{
		{
			name:  "no journal",
			app:   journal.NewVerifier[*fakeMock, struct{}](nil, nil),
			wants: journal.ErrNoJournal,
		},
		{
			name:  "no request patterns",
			app:   journal.NewVerifier[*fakeMock, struct{}](journaled(t), nil),
			give:  []vlatte.Builder[*fakeMock, struct{}]{fakeBuilder{}},
			wants: journal.ErrNoRequestPatterns,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.app.CountRequests(t.Context(), journal.Query{}, tt.give...)
			require.ErrorIs(t, err, tt.wants)

			_, err = tt.app.Requests(t.Context(), journal.Query{}, tt.give...)
			require.ErrorIs(t, err, tt.wants)

			ft := testmock.NewFakeT()
			require.False(t, tt.app.AssertRequested(ft, 0, journal.Query{}, tt.give...))
			require.Contains(t, errorText(t, ft), tt.wants.Error())

			ft = testmock.NewFakeT()
			require.False(t, tt.app.AssertNotRequested(ft, journal.Query{}, tt.give...))
			require.Contains(t, errorText(t, ft), tt.wants.Error())
		})
	}
}
