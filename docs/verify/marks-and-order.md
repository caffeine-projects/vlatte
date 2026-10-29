# Marks and order

## Marks

A check counts every request the journal holds. Tests that share one server, one after the other, see each other's requests: a test that checks `Times(t, 1)` after another test sent the same request fails with 2.

`m.JournalMark()` takes a mark of the journal as it is now, and `Since(mark)` selects the requests that came after it, and none of those before:

```go
func TestCheckout(t *testing.T) {
	mark := shared.JournalMark() // shared is a server the package's tests share

	// ... the code under test sends its requests ...

	shared.VerifyRequests().Since(mark).Path(match.Equal("/orders")).Times(t, 1)
}
```

A mark isolates tests that run one after the other. Two tests that run at the same time on one server, with `t.Parallel()`, see each other's requests after their marks: give each one its own server, or select by a header that tells their requests apart.

A journal kept in a named directory holds the requests of the server's earlier runs too, which a check without a mark counts. Tests use `Journal("")`, a temporary directory, or take a mark first.

A mark belongs to its server's journal. A mark of another server fails the check with `verify.ErrOtherJournal`, and one the journal was cleared after with `verify.ErrCleared`.

## Order

`verify.InOrder(t, steps...)` asserts that the server got a request of each step, each one after the request of the step before. Other requests may come between:

```go
login := m.VerifyRequests().Method("POST").Path(match.Equal("/login"))
order := m.VerifyRequests().Method("POST").Path(match.Equal("/orders"))
logout := m.VerifyRequests().Method("POST").Path(match.Equal("/logout"))

verify.InOrder(t, login, order, logout)
```

`verify` is `github.com/caffeine-projects/vlatte/verify`. For each step, `InOrder` takes the first of its requests after the request of the step before, so one request serves one step only. A failure names the step that had no request after the one before. Here the order came before the login:

```text
Expected requests in this order:
  1. request with method, path: #2
  2. request with method, path: none after #2, #1 before it
  3. request with method, path: not checked
```

An order is checked now. A step that waits, with `Within` or `During`, fails it: wait for the last step with its own check first, then check the order. Steps of different servers fail it too, as their journals don't share an order. Steps with `Since` count from their mark, as other checks do.
