package verify_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

// appendAfter appends e to j once d passes, as the code under test sends a request in the background.
func appendAfter(t *testing.T, j *journal.Journal, d time.Duration, e *journal.Entry) {
	t.Helper()

	go func() {
		time.Sleep(d)
		assert.NoError(t, j.Append(e))
	}()
}

func TestCheck_WithinPassesOnceTheRequestComes(t *testing.T) {
	// A request the code under test sends in the background comes after the check starts: the check waits for it, and
	// passes as it comes, not when the wait ends.
	synctest.Test(t, func(t *testing.T) {
		j := newJournal(t, journal.Options{}, entry("a"))
		appendAfter(t, j, time.Second, entry("b"))
		appendAfter(t, j, 2*time.Second, entry("c"))

		result, err := verify.Check(t.Context(), verify.Spec{
			Journal: j, Query: journal.Query{Path: match.Equal("/c")}, Count: match.Equal(1), Within: time.Minute,
		})
		require.NoError(t, err)
		require.Equal(t, verify.Result{Passed: true, Count: 1, Waited: 2 * time.Second}, result)
	})
}

func TestCheck_WithinFailsWhenItEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := verifierOf(t, entry("a"))
		appendAfter(t, v.Journal(), 3*time.Second, entry("b"))

		ft := testmock.NewFakeT()
		start := time.Now()
		require.False(t, v.VerifyRequests().Path(match.Equal("/b")).Within(2*time.Second).Times(ft, 1))
		require.Equal(t, 2*time.Second, time.Since(start))
		require.Contains(t, errorText(t, ft), "\nExpected exactly 1 request(s) with path within 2s.\nGot 0 after 2s.\n")

		// The request came too late for the wait, not never.
		time.Sleep(time.Second)
		synctest.Wait()
		require.True(t, v.VerifyRequests().Path(match.Equal("/b")).Times(t, 1))
	})
}

func TestCheck_DuringFailsOnceTheCountFails(t *testing.T) {
	// A request that must not come fails the check as it comes: the test needn't wait the rest.
	synctest.Test(t, func(t *testing.T) {
		v := verifierOf(t, entry("a"))
		appendAfter(t, v.Journal(), time.Second, entry("b"))

		ft := testmock.NewFakeT()
		start := time.Now()
		require.False(t, v.VerifyRequests().Path(match.Equal("/b")).During(time.Minute).Never(ft))
		require.Equal(t, time.Second, time.Since(start))
		require.Contains(t, errorText(t, ft), "\nExpected no request(s) with path for 1m0s.\nGot 1 after 1s.\n")
	})
}

func TestCheck_DuringPassesWhenItEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v := verifierOf(t, entry("a"))
		appendAfter(t, v.Journal(), time.Second, entry("b"))

		ft := testmock.NewFakeT()
		start := time.Now()
		require.True(t, v.VerifyRequests().Path(match.Equal("/c")).During(5*time.Second).Never(ft))
		require.Equal(t, 5*time.Second, time.Since(start))
		ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
	})
}

func TestCheck_AWaitEndsWhenTheJournalCloses(t *testing.T) {
	// A server that closes serves no more requests: a check waiting for one fails at once, with why.
	synctest.Test(t, func(t *testing.T) {
		j := newJournal(t, journal.Options{})

		go func() {
			time.Sleep(time.Second)
			j.Close()
		}()

		start := time.Now()
		_, err := verify.Check(t.Context(), verify.Spec{Journal: j, Count: match.Equal(1), Within: time.Minute})
		require.ErrorIs(t, err, journal.ErrClosed)
		require.Equal(t, time.Second, time.Since(start))
	})
}

func TestCheck_AWaitEndsWhenTheJournalIsCleared(t *testing.T) {
	// A clear, such as the admin API's reset, takes the requests a wait counted: the wait ends with why rather than
	// count again from none.
	synctest.Test(t, func(t *testing.T) {
		j := newJournal(t, journal.Options{}, entry("a"))

		go func() {
			time.Sleep(time.Second)
			assert.NoError(t, j.Clear())
		}()

		start := time.Now()
		_, err := verify.Check(t.Context(), verify.Spec{Journal: j, Count: match.Equal(2), Within: time.Minute})
		require.ErrorIs(t, err, verify.ErrCleared)
		require.Equal(t, time.Second, time.Since(start))
	})
}

func TestCheck_AWaitEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		j := newJournal(t, journal.Options{})

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		_, err := verify.Check(ctx, verify.Spec{Journal: j, Count: match.Equal(1), Within: time.Minute})
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestCheck_ARequestTheJournalDroppedFailsTheCheck(t *testing.T) {
	// A journal that keeps the newest 2 of 3 requests can't tell how many came of those a check counts: a count of
	// those left could pass when it shouldn't.
	j := newJournal(t, journal.Options{MaxEntries: 2}, entry("a"))
	v := verify.NewVerifier(j, verify.Protocol[*fakeMock, fakeServer]{})
	mark := v.JournalMark()
	require.NoError(t, j.Append(entry("b")))
	require.NoError(t, j.Append(entry("c")))

	_, err := v.VerifyRequests().Path(match.Equal("/a")).Count(t.Context())
	require.ErrorIs(t, err, verify.ErrDropped)
	require.EqualError(t, err, verify.ErrDropped.Error()+": 1 of them")

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Never(ft))
	require.Contains(t, errorText(t, ft), verify.ErrDropped.Error())

	// The journal kept every request since the mark.
	n, err := v.VerifyRequests().Since(mark).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// Not once it drops one of those.
	require.NoError(t, j.Append(entry("d")))

	_, err = v.VerifyRequests().Since(mark).Count(t.Context())
	require.ErrorIs(t, err, verify.ErrDropped)
}

func TestCheck_AMarkPastTheJournalFailsTheCheck(t *testing.T) {
	// A mark of a journal that started over, as a temporary one does when its server restarts, names requests this
	// journal never held: a count of none after it would pass whatever came.
	j := newJournal(t, journal.Options{}, entry("a"))

	_, err := verify.Check(t.Context(), verify.Spec{
		Journal: j, Query: journal.Query{After: 100}, Marked: true, Count: match.Equal(0), Expect: "no",
	})
	require.ErrorIs(t, err, verify.ErrCleared)
}

func TestCheck_AClearAsItCountsFailsTheCheck(t *testing.T) {
	// The journal cleared as a check read it took some of the requests it would count: the count it got is of part
	// of them.
	j := newJournal(t, journal.Options{}, entry("a"), entry("b"), entry("c"))
	v := verify.NewVerifier(j, verify.Protocol[*fakeMock, fakeServer]{Patterns: fakeServer{}})

	_, err := v.VerifyRequests().Matching(clearingPattern{j: j}).Count(t.Context())
	require.ErrorIs(t, err, verify.ErrCleared)

	require.NoError(t, j.Append(entry("d")))
	require.NoError(t, j.Append(entry("e")))

	_, err = verify.CheckOrder(t.Context(), verify.Spec{Journal: j, Patterns: []verify.Pattern{clearingPattern{j: j}}})
	require.ErrorIs(t, err, verify.ErrCleared)
}

// clearingPattern is a pattern that clears its journal as it matches the first entry, as a reset that comes as a
// check reads does.
type clearingPattern struct {
	j *journal.Journal
}

func (clearingPattern) Build(fakeServer) (*fakeMock, error) {
	return nil, errors.New("a pattern builds no mock")
}

func (p clearingPattern) Match(context.Context, *journal.Entry) (bool, error) {
	if p.j.Cleared() < p.j.Last() {
		if err := p.j.Clear(); err != nil {
			return false, err
		}
	}

	return true, nil
}

func (clearingPattern) Explain(context.Context, *journal.Entry) (verify.Miss, error) {
	return verify.Miss{}, nil
}

func TestCheck_ACheckOfAClearedJournalCountsWhatCameSince(t *testing.T) {
	// Without a mark, a check counts the requests the journal holds: a clear starts it over, as a test that clears the
	// server between its steps expects.
	j := newJournal(t, journal.Options{}, entry("a"))
	require.NoError(t, j.Clear())
	require.NoError(t, j.Append(entry("b")))

	result, err := verify.Check(t.Context(), verify.Spec{Journal: j, Count: match.Equal(1)})
	require.NoError(t, err)
	require.Equal(t, verify.Result{Passed: true, Count: 1}, result)
}

func TestCheck_RefusesASpecItCantRun(t *testing.T) {
	j := newJournal(t, journal.Options{})

	tests := []struct {
		name  string
		give  verify.Spec
		wants string
	}{
		{
			name:  "no journal",
			give:  verify.Spec{Count: match.Equal(1)},
			wants: journal.ErrNoJournal.Error(),
		},
		{
			name:  "no count",
			give:  verify.Spec{Journal: j},
			wants: "verify: invalid check: no count to match",
		},
		{
			name:  "a limit",
			give:  verify.Spec{Journal: j, Count: match.Equal(1), Query: journal.Query{Limit: 1}},
			wants: "verify: invalid check: a check counts all it selects: its query has no Limit or Before",
		},
		{
			name:  "a negative wait",
			give:  verify.Spec{Journal: j, Count: match.Equal(1), Within: -1},
			wants: "verify: invalid check: a wait must be longer than 0",
		},
		{
			name:  "a count that passes with none, within a wait",
			give:  verify.Spec{Journal: j, Count: match.Equal(0), Expect: "no", Within: time.Second},
			wants: "verify: invalid check: no request(s) passes with none, so Within would pass at once: use During",
		},
		{
			name: "a count matcher that fails",
			give: verify.Spec{Journal: j, Count: vlatte.MatcherFunc("boom", func(vlatte.MatcherArg) (bool, error) {
				return false, errors.New("boom")
			})},
			wants: "count: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verify.Check(t.Context(), tt.give)
			require.ErrorContains(t, err, tt.wants)

			// A count matcher that fails is the matcher's problem, as one of a query is.
			if tt.wants == "count: boom" {
				require.ErrorIs(t, err, vlatte.ErrMatching)
			}

			// A client of the admin API sends the spec: one that can't run is its problem.
			if strings.HasPrefix(tt.wants, "verify: invalid check: ") {
				require.ErrorIs(t, err, verify.ErrInvalidSpec)
			}
		})
	}
}

func TestCheck_AResultForAClientLeavesOutWhatItWaited(t *testing.T) {
	// The admin API answers a client with a result: the selected requests by number, id and line.
	j := newJournal(t, journal.Options{}, entry("a"))

	result, err := verify.Check(t.Context(), verify.Spec{Journal: j, Count: match.Equal(0), Expect: "no"})
	require.NoError(t, err)

	b, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"passed": false,
		"count": 1,
		"selected": [{"seq": 1, "id": "a", "line": "#1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200"}],
		"report": "\nExpected no request(s).\nGot 1.\n  Requests selected (1 of 1, the newest first):\n    #1 2026-10-21T12:00:00.000Z matched GET http://127.0.0.1/a 200\n"
	}`, string(b))
}
