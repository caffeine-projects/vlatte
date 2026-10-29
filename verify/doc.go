// Package verify checks the requests a mock server served, of any protocol, from its journal: which came, how many,
// by when, and in what order. The servers of [github.com/caffeine-projects/vlatte/mockhttp] and
// [github.com/caffeine-projects/vlatte/mockgrpc] embed a [Verifier] when their setup keeps a journal, so a test calls
// its methods on the server:
//
//	m := mockhttp.MustNew(mockhttp.Setup().Journal("")) // "" keeps the journal in a temporary directory
//	// ... the code under test sends its requests to m ...
//	m.VerifyRequests().Method("POST").Path(match.Equal("/orders")).Times(t, 1)
//
// # Selecting
//
// [Verifier.VerifyRequests] selects every request the journal holds, whatever its outcome. Criteria narrow the
// selection by the fields every protocol has: [Requests.Method], [Requests.Path], [Requests.Target],
// [Requests.Header], [Requests.Body], [Requests.Status], [Requests.Outcome] and [Requests.MockID]. They are the keys of
// the admin API's queries too. [Requests.Matching] selects by the request part of the server's own mocks, for what no
// criterion says, such as an HTTP query parameter or form field.
//
// A check counts from the start of the journal, or from a mark [Verifier.JournalMark] took, which [Requests.Since]
// names: a test that shares a server with others before it counts its own requests only. A journal kept in a
// directory holds the requests of the server's earlier runs too, which a check without a mark counts.
//
// # Checking
//
// [Requests.Times], [Requests.AtLeast], [Requests.AtMost], [Requests.Never] and [Requests.CountIs] assert the number of
// requests a selection holds, and [InOrder] the order of the requests of several. A check runs now, or waits:
// [Requests.Within] until it passes, for a request the code under test sends in the background, and [Requests.During]
// as long as it holds. A wait wakes as each request comes, rather than poll.
//
// A failure lists the requests the selection holds and, when it wanted more, those that came closest, with the parts
// they missed: one line each, without their headers or body, which may hold secrets.
//
// A check never passes on part of the requests it would count. One whose window holds requests the journal dropped
// to keep its MaxEntries fails with [ErrDropped]; one of a journal cleared after its mark or as it reads, or from a
// mark past the journal's newest request, as of a journal that started over, with [ErrCleared]. A check of a body
// the journal cut, once the rest of it matches, fails with
// [github.com/caffeine-projects/vlatte/journal.ErrBodyCut].
//
// [Check] and [CheckOrder] run checks [Spec] values describe, for those that verify other than in a test, such as the
// admin API's QUERY /requests/verify.
package verify
