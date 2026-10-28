package verify_test

import (
	"context"
	"errors"
	"testing"

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
