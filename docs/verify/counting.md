# Counts and reports

An assertion counts the requests a selection holds, now or over a [wait](/verify/waiting), and reports a failure with `t.Errorf`, as `testing.T` does, so the test goes on. Each returns `true` when it passed:

| Assertion | Passes when the selection holds |
|---|---|
| `Times(t, n)` | Exactly `n` requests. |
| `AtLeast(t, n)` | `n` requests or more. |
| `AtMost(t, n)` | `n` requests or fewer. |
| `Never(t)` | No request. |
| `CountIs(t, m)` | A number of requests `m`, a matcher of an `int`, matches, such as `match.LTE(3)`. |

```go
if !m.VerifyRequests().Path(match.Equal("/login")).Times(t, 1) {
	t.FailNow() // the rest of the test needs the login
}

m.VerifyRequests().Path(match.HasPrefix("/retry")).CountIs(t, match.All(match.GTE(1), match.LTE(3)))
```

`t` is a `vlatte.TestingT`, such as a `*testing.T`. An assertion runs with a context of its own, so one in a `t.Cleanup` works once the test's context is done.

## The failure report

A failure says what the check expected and what it got. It lists the requests the selection holds, the newest first, ten at most. When the check wanted more requests than it got, it lists the requests of the journal that came closest too, ranked by the parts of the selection they missed, with the parts each missed:

```text
Expected exactly 1 request(s) with method, path.
Got 0.
  Closest requests (2 of 2 the server served, the newest first):
    #2 2026-10-29T14:05:55.699Z unmatched POST http://127.0.0.1:34207/order 418, closest [create-order] differs in path
        missed 1 of 2: path
    #1 2026-10-29T14:05:55.698Z matched PUT http://127.0.0.1:34207/orders 200 [update-order]
        missed 1 of 2: method
```

Each request has a line: its number in the journal, its time, outcome, method, URL and status, the id of the mock that answered it, and, for a request no mock matched, the mock that came closest and what differed. A line leaves out the headers and the body, which may hold secrets: the number and the request's id, from its `VLatte-Request-Id` header, get the rest of it from the journal. The closest requests come from the newest 100 of the journal.

A check that wanted fewer requests lists the ones it selected only, as those are the ones that should not have come.

## Reading the requests

`Find` returns the requests a selection holds, the newest first, with what the journal kept of each, and `Count` returns how many. Neither waits nor asserts:

```go
orders := m.VerifyRequests().Method("POST").Path(match.Equal("/orders"))

entries, err := orders.Find(ctx)
n, err := orders.Count(ctx)
```

A selection has no limit, so `Find` returns every request it holds. `m.Journal().Find` takes a [`journal.Query`](/features/journal) with a `Limit`, and pages through the requests instead. Like a check, `Find` and `Count` fail rather than return part of the requests: see [Troubleshooting](/verify/troubleshooting).
