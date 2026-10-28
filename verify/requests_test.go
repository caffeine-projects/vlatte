package verify_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

// served returns the verifier of a server that served a GET of /a, a POST of /orders with a JSON body that a mock
// answered, and a DELETE of /orders/1 no mock matched.
func served(t *testing.T) *verify.Verifier[*fakeMock, fakeServer] {
	t.Helper()

	return verifierOf(t,
		entry("a", func(e *journal.Entry) { e.Header = map[string][]string{"Accept": {"text/plain"}} }),
		entry("b", func(e *journal.Entry) {
			e.Method, e.Path, e.Target, e.Status, e.MockID = "POST", "/orders", "http://127.0.0.1/orders?dry=1", 201, "add-order"
			e.Header = map[string][]string{"Content-Type": {"application/json"}, "Accept": {"application/json"}}
			e.Body = []byte(`{"item": "tea"}`)
		}),
		entry("c", func(e *journal.Entry) {
			e.Method, e.Path, e.Status, e.Outcome = "DELETE", "/orders/1", 404, journal.OutcomeUnmatched
		}),
	)
}

func TestRequests_CriteriaNarrowTheSelection(t *testing.T) {
	// Each criterion selects by a field every protocol has; all of a selection's must match.
	v := served(t)

	tests := []struct {
		name string
		give verify.Requests[*fakeMock, fakeServer]
		want []string
	}{
		{
			name: "none selects every request",
			give: v.VerifyRequests(),
			want: []string{"c", "b", "a"},
		},
		{
			name: "method in any case",
			give: v.VerifyRequests().Method("post"),
			want: []string{"b"},
		},
		{
			name: "any of several methods",
			give: v.VerifyRequests().Method("POST", "DELETE"),
			want: []string{"c", "b"},
		},
		{
			name: "path",
			give: v.VerifyRequests().Path(match.HasPrefix("/orders")),
			want: []string{"c", "b"},
		},
		{
			name: "target",
			give: v.VerifyRequests().Target(match.Equal("http://127.0.0.1/orders?dry=1")),
			want: []string{"b"},
		},
		{
			name: "header in any case",
			give: v.VerifyRequests().Header("accept", match.Equal("application/json")),
			want: []string{"b"},
		},
		{
			name: "a header the request lacks matches as empty",
			give: v.VerifyRequests().Header("Accept", match.Equal("")),
			want: []string{"c"},
		},
		{
			name: "every header",
			give: v.VerifyRequests().
				Header("Accept", match.Equal("application/json")).
				Header("Content-Type", match.Equal("text/plain")),
			want: []string{},
		},
		{
			name: "body",
			give: v.VerifyRequests().Body(match.JSONPath("item", match.Equal("tea"))),
			want: []string{"b"},
		},
		{
			name: "status",
			give: v.VerifyRequests().Status(match.Equal(404)),
			want: []string{"c"},
		},
		{
			name: "outcome",
			give: v.VerifyRequests().Outcome(journal.OutcomeUnmatched),
			want: []string{"c"},
		},
		{
			name: "any of several outcomes",
			give: v.VerifyRequests().Outcome(journal.OutcomeUnmatched, journal.OutcomeMatched),
			want: []string{"c", "b", "a"},
		},
		{
			name: "mock id",
			give: v.VerifyRequests().MockID(match.Equal("add-order")),
			want: []string{"b"},
		},
		{
			name: "a pattern of the server's mocks",
			give: v.VerifyRequests().Matching(pathPattern("/a")),
			want: []string{"a"},
		},
		{
			name: "every pattern",
			give: v.VerifyRequests().Matching(pathPattern("/a"), pathPattern("/orders")),
			want: []string{},
		},
		{
			name: "every criterion",
			give: v.VerifyRequests().Method("DELETE").Path(match.HasPrefix("/orders")).Status(match.Equal(200)),
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ids(t)(tt.give.Find(t.Context())))

			n, err := tt.give.Count(t.Context())
			require.NoError(t, err)
			require.Equal(t, len(tt.want), n)
		})
	}
}

func TestRequests_ASelectionIsAValue(t *testing.T) {
	// A test names a selection and narrows it in several ways: one way never changes another.
	v := served(t)
	orders := v.VerifyRequests().Path(match.HasPrefix("/orders")).Header("Accept", match.Equal("application/json"))

	posts := orders.Method("POST")
	deletes := orders.Method("DELETE")
	cut := orders.Header("Content-Type", match.Equal("text/plain"))
	matched := orders.Outcome(journal.OutcomeMatched)
	unmatched := orders.Outcome(journal.OutcomeUnmatched)

	require.Equal(t, []string{"b"}, ids(t)(orders.Find(t.Context())))
	require.Equal(t, []string{"b"}, ids(t)(posts.Find(t.Context())))
	require.Equal(t, []string{}, ids(t)(deletes.Find(t.Context())))
	require.Equal(t, []string{}, ids(t)(cut.Find(t.Context())))
	require.Equal(t, []string{"b"}, ids(t)(matched.Find(t.Context())))
	require.Equal(t, []string{}, ids(t)(unmatched.Find(t.Context())))

	a := v.VerifyRequests().Matching(pathPattern("/a"))
	_ = a.Matching(pathPattern("/orders"))
	require.Equal(t, []string{"a"}, ids(t)(a.Find(t.Context())))
}

func TestRequests_AssertTheNumberOfRequests(t *testing.T) {
	// The server served two requests of /orders.
	orders := served(t).VerifyRequests().Path(match.HasPrefix("/orders"))

	tests := []struct {
		name   string
		assert func(t vlatte.TestingT) bool
		want   bool
	}{
		{name: "times", assert: func(t vlatte.TestingT) bool { return orders.Times(t, 2) }, want: true},
		{name: "times fewer", assert: func(t vlatte.TestingT) bool { return orders.Times(t, 1) }, want: false},
		{name: "times more", assert: func(t vlatte.TestingT) bool { return orders.Times(t, 3) }, want: false},
		{name: "at least", assert: func(t vlatte.TestingT) bool { return orders.AtLeast(t, 2) }, want: true},
		{name: "at least more", assert: func(t vlatte.TestingT) bool { return orders.AtLeast(t, 3) }, want: false},
		{name: "at most", assert: func(t vlatte.TestingT) bool { return orders.AtMost(t, 2) }, want: true},
		{name: "at most fewer", assert: func(t vlatte.TestingT) bool { return orders.AtMost(t, 1) }, want: false},
		{name: "never", assert: func(t vlatte.TestingT) bool { return orders.Method("PUT").Never(t) }, want: true},
		{name: "never of some", assert: func(t vlatte.TestingT) bool { return orders.Never(t) }, want: false},
		{
			name:   "count is",
			assert: func(t vlatte.TestingT) bool { return orders.CountIs(t, match.Any(match.Equal(2), match.Equal(5))) },
			want:   true,
		},
		{
			name:   "count is not",
			assert: func(t vlatte.TestingT) bool { return orders.CountIs(t, match.Equal(5)) },
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := testmock.NewFakeT()
			require.Equal(t, tt.want, tt.assert(ft))

			if tt.want {
				ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
			} else {
				require.Contains(t, errorText(t, ft), "\nGot 2.\n")
			}
		})
	}
}

func TestRequests_ASelectionItCantTakeFailsTheCheck(t *testing.T) {
	// A misuse fails the check with why, never passes it: a criterion that selected nothing would pass Never.
	v := served(t)

	tests := []struct {
		name  string
		give  verify.Requests[*fakeMock, fakeServer]
		wants string
	}{
		{
			name:  "no method",
			give:  v.VerifyRequests().Method(),
			wants: "verify: Method needs a method",
		},
		{
			name:  "nil matcher",
			give:  v.VerifyRequests().Header("Accept", nil),
			wants: "verify: Header Accept needs a matcher",
		},
		{
			name:  "no outcome",
			give:  v.VerifyRequests().Outcome(),
			wants: "verify: Outcome needs an outcome",
		},
		{
			name:  "an outcome that is none",
			give:  v.VerifyRequests().Outcome(journal.Outcome(99)),
			wants: "journal: outcome 99 is not one of matched, unmatched, proxied, failed or abandoned",
		},
		{
			name:  "one of several outcomes that is none",
			give:  v.VerifyRequests().Outcome(journal.OutcomeMatched, journal.Outcome(99)),
			wants: "verify: invalid check: outcome 99 is not one of matched, unmatched, proxied, failed or abandoned",
		},
		{
			name:  "a wait of 0",
			give:  v.VerifyRequests().Within(0),
			wants: "verify: Within must be longer than 0, not 0s",
		},
		{
			name:  "a negative wait",
			give:  v.VerifyRequests().During(-1),
			wants: "verify: During must be longer than 0, not -1ns",
		},
		{
			name:  "both waits",
			give:  v.VerifyRequests().Within(1).During(1),
			wants: "verify: invalid check: it waits Within or During, not both",
		},
		{
			name:  "a builder the protocol takes no pattern of",
			give:  v.VerifyRequests().Matching(pathPattern("/a"), otherBuilder{}),
			wants: "verify: pattern 1: not a pattern",
		},
		{
			name:  "the first misuse counts",
			give:  v.VerifyRequests().Outcome().Method(),
			wants: "verify: Outcome needs an outcome",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := testmock.NewFakeT()
			require.False(t, tt.give.Never(ft))
			require.Equal(t, "\nCould not verify the requests.\n"+tt.wants, errorText(t, ft))
		})
	}
}

func TestRequests_AnAssertionItCantTakeFailsTheCheck(t *testing.T) {
	r := served(t).VerifyRequests()

	ft := testmock.NewFakeT()
	require.False(t, r.Times(ft, -1))
	require.Equal(t, "\nCould not verify the requests.\nverify: a count must be 0 or more, not -1", errorText(t, ft))

	ft = testmock.NewFakeT()
	require.False(t, r.CountIs(ft, nil))
	require.Equal(t, "\nCould not verify the requests.\nverify: CountIs needs a matcher", errorText(t, ft))

	// A count that passes with none passes at once: there is nothing to wait for.
	ft = testmock.NewFakeT()
	require.False(t, r.Within(1).AtMost(ft, 1))
	require.Equal(t, "\nCould not verify the requests.\n"+
		"verify: invalid check: at most 1 request(s) passes with none, so Within would pass at once: use During", errorText(t, ft))
}

func TestRequests_AProtocolWithoutMethodsRefusesMethod(t *testing.T) {
	// A gRPC call has no method: a selection by one would select none, and Never would pass.
	v := verify.NewVerifier(newJournal(t, journal.Options{}), verify.Protocol[*fakeMock, fakeServer]{NoMethod: true})

	_, err := v.VerifyRequests().Method("POST").Count(t.Context())
	require.ErrorIs(t, err, verify.ErrNoMethod)

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Method("POST").Never(ft))
	require.Contains(t, errorText(t, ft), verify.ErrNoMethod.Error())
}

func TestRequests_ABodyTheJournalCutCantTell(t *testing.T) {
	// The part of the body the journal cut may hold what the matcher looks for: the check can't say no.
	j := newJournal(t, journal.Options{MaxBodySize: 4}, entry("a", func(e *journal.Entry) { e.Body = []byte("123456") }))
	v := verify.NewVerifier(j, verify.Protocol[*fakeMock, fakeServer]{})

	_, err := v.VerifyRequests().Body(match.HasSuffix("56")).Count(t.Context())
	require.ErrorIs(t, err, vlatte.ErrMatching)

	// A selection the rest of the entry fails never reads its body.
	n, err := v.VerifyRequests().Path(match.Equal("/b")).Body(match.HasSuffix("56")).Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestRequests_AssertsInACleanup(t *testing.T) {
	// A test checks its requests once it ends, in a cleanup, when its context is done: the check still runs.
	v := verifierOf(t, entry("a"))

	t.Run("cleanup", func(t *testing.T) {
		t.Cleanup(func() {
			require.Error(t, t.Context().Err())
			require.True(t, v.VerifyRequests().Path(match.Equal("/a")).Times(t, 1))
		})
	})
}

func TestRequests_AFailureNamesWhatItSelectsBy(t *testing.T) {
	v := served(t)

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().
		Method("PUT").
		Header("X-B", match.Equal("1")).
		Header("X-A", match.Equal("1")).
		Matching(pathPattern("/a")).
		Times(ft, 1))

	report := errorText(t, ft)
	require.True(t, strings.HasPrefix(report,
		"\nExpected exactly 1 request(s) with method, header X-A, header X-B, a pattern.\nGot 0.\n"), report)
}
