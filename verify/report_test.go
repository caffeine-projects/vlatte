package verify_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

func TestReport_ListsTheRequestsSelectedTheNewestFirst(t *testing.T) {
	// A check that got too many tells which: the closest are of no use when none should have come.
	v := verifierOf(t,
		entry("a"),
		entry("b", func(e *journal.Entry) {
			e.Outcome, e.Status = journal.OutcomeUnmatched, 418
			e.NearMisses = []vlatte.NearMiss{{MockID: "m1", Passed: 1, Total: 2, Failed: []string{"header Accept"}}}
		}),
		entry("c", func(e *journal.Entry) { e.Method = "POST" }),
	)

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Method("GET").AtMost(ft, 1))
	require.Equal(t, "\nExpected at most 1 request(s) with method.\nGot 2.\n"+
		"  Requests selected (2 of 2, the newest first):\n"+
		"    #2 2026-10-21T12:00:00.000Z unmatched GET http://127.0.0.1/b 418, closest [m1] differs in header Accept\n"+
		"    #1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200\n",
		errorText(t, ft))
}

func TestReport_NamesEveryCriterionThatExcludedARequest(t *testing.T) {
	j := newJournal(t, journal.Options{}, entry("a"))
	tests := []struct {
		name  string
		query journal.Query
	}{
		{name: "kind", query: journal.Query{Kind: "grpc"}},
		{name: "outcome", query: journal.Query{Outcome: journal.OutcomeFailed}},
		{name: "mock id", query: journal.Query{MockID: match.Equal("missing")}},
		{name: "target", query: journal.Query{Target: match.Equal("http://elsewhere/a")}},
		{name: "status", query: journal.Query{Status: match.Equal(404)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := verify.Check(t.Context(), verify.Spec{Journal: j, Query: tt.query, Count: match.Equal(1), Expect: "exactly 1"})
			require.NoError(t, err)
			require.False(t, result.Passed)
			require.Equal(t, 0, result.Count)
			require.Equal(t, []verify.Listed{{Seq: 1, ID: "a", Line: "#1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200", Total: 1, Missed: []string{tt.name}}}, result.Closest)
		})
	}
}

func TestReport_NamesMultiplePatternsAndTheTimeWindow(t *testing.T) {
	j := newJournal(t, journal.Options{}, entry("a"))
	result, err := verify.Check(t.Context(), verify.Spec{
		Journal: j, Query: journal.Query{Since: _t0.Add(-time.Second)},
		Patterns: []verify.Pattern{pathPattern("/b"), pathPattern("/c")}, Count: match.Equal(1), Expect: "exactly 1",
	})
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Equal(t, "\nExpected exactly 1 request(s) with 2 patterns, time.\nGot 0.\n"+
		"  Closest requests (1 of 1 the server served, the newest first):\n"+
		"    #1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200\n"+
		"        missed 2 of 2: pattern path, pattern path\n", result.Report)
	require.Equal(t, []verify.Listed{{Seq: 1, ID: "a", Line: "#1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200", Total: 2, Missed: []string{"pattern path", "pattern path"}}}, result.Closest)
}

func TestReport_DoesNotRankATruncatedBodyAsAMiss(t *testing.T) {
	j := newJournal(t, journal.Options{MaxBodySize: 1}, entry("a", func(e *journal.Entry) { e.Body = []byte("tea") }))
	result, err := verify.Check(t.Context(), verify.Spec{
		Journal: j, Query: journal.Query{Path: match.Equal("/b"), Body: match.Equal("coffee")}, Count: match.Equal(1),
	})
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Equal(t, []verify.Listed{{Seq: 1, ID: "a", Line: "#1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200 (body cut)", Total: 1, Missed: []string{"path"}}}, result.Closest)
}

func TestReport_ACountMatcherFailureDoesNotProduceAMisleadingReport(t *testing.T) {
	errCount := errors.New("test: count unavailable")
	j := newJournal(t, journal.Options{})
	count := vlatte.MatcherFunc("count", func(arg vlatte.MatcherArg) (bool, error) {
		if arg.Value() == 0 {
			return false, nil
		}
		return false, errCount
	})
	_, err := verify.Check(t.Context(), verify.Spec{Journal: j, Count: count})
	require.ErrorIs(t, err, errCount)
	require.ErrorIs(t, err, vlatte.ErrMatching)
}

func TestReport_AnUnreadableJournalDoesNotProduceAMisleadingReport(t *testing.T) {
	errRead := errors.New("test: journal unavailable")
	j := &unreadableJournal{Journal: newJournal(t, journal.Options{}, entry("a")), err: errRead}
	_, err := verify.Check(t.Context(), verify.Spec{Journal: j, Count: match.Equal(0)})
	require.ErrorIs(t, err, errRead)
}

type unreadableJournal struct {
	*journal.Journal

	err error
}

func (j *unreadableJournal) Find(context.Context, journal.Query, ...journal.Filter) (journal.Page, error) {
	return journal.Page{}, j.err
}

func TestReport_TellsWhatCameOfEachRequest(t *testing.T) {
	// A request's line says what a test needs to tell why it was or wasn't the one expected: the mock that answered
	// it, a body the journal cut, what failed, and the mock that came closest, which only a part that keeps state,
	// such as Times, kept from matching.
	j := newJournal(t, journal.Options{MaxBodySize: 1},
		entry("a", func(e *journal.Entry) { e.MockID, e.Body = "m1", []byte("cut") }),
		entry("b", func(e *journal.Entry) { e.Outcome, e.Status, e.Message = journal.OutcomeFailed, 500, "reply: boom" }),
		entry("c", func(e *journal.Entry) {
			e.Outcome, e.Status = journal.OutcomeUnmatched, 418
			e.NearMisses = []vlatte.NearMiss{{MockID: "m2", Passed: 2, Total: 2, Skipped: []string{"times"}}}
		}),
		entry("d", func(e *journal.Entry) {
			e.MockID, e.Message = "m3", "webhook at index 0: target down\nwebhook at index 1: timeout"
		}),
	)
	v := verify.NewVerifier(j, verify.Protocol[*fakeMock, fakeServer]{})

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Never(ft))

	report := errorText(t, ft)
	// A message of many lines, such as the on_complete events that failed, stays on the line of its request.
	require.Contains(t, report,
		"matched GET http://127.0.0.1/d 200 [m3]: webhook at index 0: target down; webhook at index 1: timeout\n")
	require.Contains(t, report, "unmatched GET http://127.0.0.1/c 418, closest [m2] missed by times\n")
	require.Contains(t, report, "failed GET http://127.0.0.1/b 500: reply: boom\n")
	require.Contains(t, report, "matched GET http://127.0.0.1/a 200 [m1] (body cut)\n")
}

func TestReport_RanksTheClosestByThePartsTheyMissed(t *testing.T) {
	// A request the test expected that came with a part wrong is the one that missed the fewest parts: it comes first,
	// with the parts it missed, so the test tells which part was wrong.
	v := verifierOf(t,
		entry("a", func(e *journal.Entry) { e.Method, e.Path = "POST", "/orders" }),
		entry("b", func(e *journal.Entry) {
			e.Method, e.Path = "PUT", "/orders"
			e.Header = map[string][]string{"Content-Type": {"application/json"}}
		}),
		entry("c", func(e *journal.Entry) {
			e.Method, e.Path = "POST", "/orders"
			e.Header = map[string][]string{"Content-Type": {"application/json"}}
		}),
		entry("d"),
	)

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().
		Method("POST").
		Path(match.Equal("/orders")).
		Header("Content-Type", match.Equal("application/json")).
		Matching(pathPattern("/orders")).
		Times(ft, 2))

	require.Equal(t, "\nExpected exactly 2 request(s) with method, path, header Content-Type, a pattern.\nGot 1.\n"+
		"  Requests selected (1 of 1, the newest first):\n"+
		"    #3 2026-10-21T12:00:00.000Z matched POST http://127.0.0.1/c 200\n"+
		"  Closest requests (3 of 4 the server served, the newest first):\n"+
		"    #2 2026-10-21T12:00:00.000Z matched PUT http://127.0.0.1/b 200\n"+
		"        missed 1 of 4: method\n"+
		"    #1 2026-10-21T12:00:00.000Z matched POST http://127.0.0.1/a 200\n"+
		"        missed 1 of 4: header Content-Type\n"+
		"    #4 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/d 200\n"+
		"        missed 4 of 4: method, path, header Content-Type, pattern path\n",
		errorText(t, ft))
}

func TestReport_LeavesOutHeadersAndBodies(t *testing.T) {
	// A report goes to test logs and CI, where a header or body, such as a token, must not.
	v := verifierOf(t, entry("a", func(e *journal.Entry) {
		e.Header = map[string][]string{"Authorization": {"Bearer secret-token"}}
		e.Body = []byte(`{"password": "secret-password"}`)
	}))

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().
		Header("Authorization", match.Equal("Bearer other")).
		Body(match.HasPrefix("x")).
		AtLeast(ft, 1))

	report := errorText(t, ft)
	require.Contains(t, report, "missed 2 of 2: header Authorization, body\n")
	require.NotContains(t, report, "secret")
}

func TestReport_SaysWhenTheServerServedNothing(t *testing.T) {
	v := verifierOf(t)

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Times(ft, 1))
	require.Equal(t, "\nExpected exactly 1 request(s).\nGot 0.\n  Requests the server served: none\n", errorText(t, ft))
}

func TestReport_NamesTheMarkItCountsFrom(t *testing.T) {
	// The requests before the mark are other tests': the closest are of those after it only.
	v := verifierOf(t, entry("a"))
	mark := v.JournalMark()

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Since(mark).Times(ft, 1))
	require.Equal(t, "\nExpected exactly 1 request(s).\nGot 0.\n  Requests since mark #1: none\n", errorText(t, ft))

	require.NoError(t, v.Journal().Append(entry("b")))

	ft = testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Since(mark).Path(match.Equal("/c")).Times(ft, 1))
	require.Equal(t, "\nExpected exactly 1 request(s) with path.\nGot 0.\n"+
		"  Closest requests (1 of 1 since mark #1, the newest first):\n"+
		"    #2 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/b 200\n"+
		"        missed 1 of 1: path\n",
		errorText(t, ft))
}

func TestReport_TheClosestLeaveOutEveryRequestSelected(t *testing.T) {
	// More requests selected than the report lists are no near misses: listed among the closest, with no part missed,
	// they would push out the request that tells why the check failed.
	entries := make([]*journal.Entry, 0, 13)
	for i := range 12 {
		entries = append(entries, entry(strconv.Itoa(i), func(e *journal.Entry) { e.Method = "POST" }))
	}

	j := newJournal(t, journal.Options{}, append(entries, entry("put", func(e *journal.Entry) { e.Method = "PUT" }))...)

	result, err := verify.Check(t.Context(), verify.Spec{
		Journal: j, Query: journal.Query{Method: match.Equal("POST")}, Count: match.Equal(13), Expect: "exactly 13",
	})
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.Len(t, result.Selected, 10)
	require.Equal(t, []verify.Listed{{
		Seq: 13, ID: "put", Line: "#13 2026-10-21T12:00:00.000Z matched PUT http://127.0.0.1/put 200",
		Total: 1, Missed: []string{"method"},
	}}, result.Closest)
}

func TestReport_StopsWhenTheCheckIsCanceled(t *testing.T) {
	// A client that leaves, or an admin API that closes, ends the report of a check that failed: its matchers may wait
	// on the context.
	j := newJournal(t, journal.Options{}, entry("a"), entry("b"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err := verify.Check(ctx, verify.Spec{
		Journal: j, Patterns: []verify.Pattern{&cancelingPattern{cancel: cancel}}, Count: match.Equal(1), Expect: "exactly 1",
	})
	require.ErrorIs(t, err, context.Canceled)
}

// cancelingPattern is a pattern that matches no request and cancels the check as it tells why the first missed, as a
// client that leaves does: it tells why the next missed only while the check's context isn't done.
type cancelingPattern struct {
	cancel context.CancelFunc
	told   bool
}

func (*cancelingPattern) Build(fakeServer) (*fakeMock, error) {
	return nil, errors.New("a pattern builds no mock")
}

func (*cancelingPattern) Match(context.Context, *journal.Entry) (bool, error) { return false, nil }

func (p *cancelingPattern) Explain(ctx context.Context, _ *journal.Entry) (verify.Miss, error) {
	if p.told {
		return verify.Miss{}, ctx.Err()
	}

	p.told = true
	p.cancel()

	return verify.Miss{Total: 1, Failed: []string{"pattern"}}, nil
}

func TestReport_AReportThatCantRankTheRequestsSaysWhy(t *testing.T) {
	// A pattern that fails on a request it ranks fails the check: a report without that request would hide why.
	v := verifierOf(t, entry("a"))

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Matching(brokenPattern{}).Times(ft, 1))
	require.Equal(t, "\nCould not verify the requests.\nboom", errorText(t, ft))
}

// brokenPattern is a pattern that matches no request, and fails to tell why.
type brokenPattern struct{}

func (brokenPattern) Build(fakeServer) (*fakeMock, error) {
	return nil, errors.New("a pattern builds no mock")
}

func (brokenPattern) Match(context.Context, *journal.Entry) (bool, error) { return false, nil }

func (brokenPattern) Explain(context.Context, *journal.Entry) (verify.Miss, error) {
	return verify.Miss{}, errors.New("boom")
}
