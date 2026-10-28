package verify_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

// session returns the verifier of a server that got a login, a health check, an order and a logout, in that order.
func session(t *testing.T) *verify.Verifier[*fakeMock, fakeServer] {
	t.Helper()

	return verifierOf(t,
		entry("login", func(e *journal.Entry) { e.Method = "POST" }),
		entry("health"),
		entry("orders", func(e *journal.Entry) { e.Method = "POST" }),
		entry("logout", func(e *journal.Entry) { e.Method = "POST" }),
	)
}

func TestInOrder_PassesWhenEachStepCameAfterTheOneBefore(t *testing.T) {
	// Other requests, such as a health check, may come between the steps.
	v := session(t)
	login := v.VerifyRequests().Path(match.Equal("/login"))
	orders := v.VerifyRequests().Path(match.Equal("/orders"))
	logout := v.VerifyRequests().Path(match.Equal("/logout"))

	ft := testmock.NewFakeT()
	require.True(t, verify.InOrder(ft, login, orders, logout))
	require.True(t, verify.InOrder(ft, login, logout))

	// A step may select a request of several: the first after the step before.
	require.True(t, verify.InOrder(ft, login, v.VerifyRequests().Method("POST"), logout))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestInOrder_FailsWhenAStepCameBeforeTheOneBefore(t *testing.T) {
	// The report says which step failed and where its requests came: the steps after it aren't checked.
	v := session(t)
	login := v.VerifyRequests().Path(match.Equal("/login"))
	orders := v.VerifyRequests().Path(match.Equal("/orders"))
	logout := v.VerifyRequests().Path(match.Equal("/logout"))

	ft := testmock.NewFakeT()
	require.False(t, verify.InOrder(ft, logout, orders, login))
	require.Equal(t, "\nExpected requests in this order:\n"+
		"  1. request with path: #4\n"+
		"  2. request with path: none after #4, #3 before it\n"+
		"  3. request with path: not checked\n",
		errorText(t, ft))
}

func TestInOrder_FailsWhenAStepHasNoRequest(t *testing.T) {
	v := session(t)

	ft := testmock.NewFakeT()
	require.False(t, verify.InOrder(ft,
		v.VerifyRequests().Path(match.Equal("/login")),
		v.VerifyRequests().Path(match.Equal("/cart"))))
	require.Equal(t, "\nExpected requests in this order:\n"+
		"  1. request with path: #1\n"+
		"  2. request with path: none\n",
		errorText(t, ft))
}

func TestInOrder_ARequestServesOneStep(t *testing.T) {
	// Two steps of one request need two of them: the second login comes after the first.
	v := session(t)
	login := v.VerifyRequests().Path(match.Equal("/login"))

	ft := testmock.NewFakeT()
	require.False(t, verify.InOrder(ft, login, login))
	require.Equal(t, "\nExpected requests in this order:\n"+
		"  1. request with path: #1\n"+
		"  2. request with path: none after #1, #1 before it\n",
		errorText(t, ft))

	posts := v.VerifyRequests().Method("POST")
	require.False(t, verify.InOrder(testmock.NewFakeT(), posts, posts, posts, posts))
	require.True(t, verify.InOrder(t, posts, posts, posts))
}

func TestInOrder_RefusesStepsItCantOrder(t *testing.T) {
	v := session(t)
	other := verifierOf(t, entry("login"))
	login := v.VerifyRequests().Path(match.Equal("/login"))

	tests := []struct {
		name  string
		give  []verify.Requests[*fakeMock, fakeServer]
		wants string
	}{
		{
			name:  "no steps",
			wants: "verify: invalid check: an order needs a step",
		},
		{
			name:  "a step that waits",
			give:  []verify.Requests[*fakeMock, fakeServer]{login, login.Within(time.Second)},
			wants: "verify: invalid check: step 2 waits: an order is checked now, so wait for the last step first",
		},
		{
			name:  "steps of two servers",
			give:  []verify.Requests[*fakeMock, fakeServer]{login, other.VerifyRequests()},
			wants: "verify: invalid check: step 2 is of another server's journal: their orders don't compare",
		},
		{
			name:  "a step it can't select by",
			give:  []verify.Requests[*fakeMock, fakeServer]{login.Method()},
			wants: "verify: step 1: verify: Method needs a method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ft := testmock.NewFakeT()
			require.False(t, verify.InOrder(ft, tt.give...))
			require.Equal(t, "\nCould not verify the order of the requests.\n"+tt.wants, errorText(t, ft))
		})
	}
}

func TestInOrder_RefusesStepsOfRequestsTheJournalDropped(t *testing.T) {
	// The login may be among those dropped: an order of those left could pass when it shouldn't.
	j := newJournal(t, journal.Options{MaxEntries: 1}, entry("login"), entry("logout"))
	v := verify.NewVerifier(j, verify.Protocol[*fakeMock, fakeServer]{})

	ft := testmock.NewFakeT()
	require.False(t, verify.InOrder(ft, v.VerifyRequests().Path(match.Equal("/logout"))))
	require.Equal(t, "\nCould not verify the order of the requests.\nverify: step 1: "+verify.ErrDropped.Error()+": 1 of them",
		errorText(t, ft))
}
