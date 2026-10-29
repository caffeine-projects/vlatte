# Waiting

A request is journaled as the server ends its answer, as hits count. A client gets a small answer only then, so a check right after the call that sent the request finds it. A request the code under test sends in the background, such as a webhook, a retry or a call from a goroutine, may not have come yet when the test checks. Neither may a stream, or a large answer whose start the client reads first. Those checks wait.

## Within

`Within(d)` waits up to `d` for the count to pass. It checks at once, then again as each request comes, and passes as soon as the count does. It fails when `d` ends:

```go
// The code under test calls the payment webhook in the background.
m.VerifyRequests().Path(match.Equal("/hooks/paid")).Within(5 * time.Second).Times(t, 1)
```

The wait wakes when the journal changes, rather than poll, so a check passes the moment its request comes, and a long `d` costs nothing when it does.

A count that passes with no request, such as `Never`'s or `AtMost`'s, would pass `Within` at once, so it fails the check: use `During`.

## During

`During(d)` holds the count for `d`. It fails as soon as the count fails, and passes when `d` ends. It tells that something did not happen in that time:

```go
// No retry within a second of the failure.
m.VerifyRequests().Path(match.Equal("/charges")).During(time.Second).AtMost(t, 1)

m.VerifyRequests().Outcome(journal.OutcomeUnmatched).During(500 * time.Millisecond).Never(t)
```

## What ends a wait

- A clear of the journal, such as `POST /reset` of the admin API or `m.Journal().Clear()`, fails the check with `verify.ErrCleared`: the requests the check had counted are gone.
- A server that closes fails it with `journal.ErrClosed`.

A wait can't take part of a [check of an order](/verify/marks-and-order#order): wait for the last step with its own check first, then check the order.

