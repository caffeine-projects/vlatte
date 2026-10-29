# Verifying requests

A test checks what the code under test sent: which requests came, how many, by when, and in what order. A server with a [journal](/features/journal) keeps every request it gets, whatever answered it, and `m.VerifyRequests()` selects and counts them, on HTTP and gRPC servers alike. A failed check says why: the requests it selected and, when it wanted more, those that came closest, with the parts each one missed.

::: code-group

```go [HTTP]
m := mockhttp.MustNew(mockhttp.Setup().Journal("")) // "" keeps the journal in a temporary directory
m.MustStart()
t.Cleanup(m.Close)

// ... the code under test sends its requests ...

m.VerifyRequests().Method("POST").Path(match.Equal("/orders")).Times(t, 1)
m.VerifyRequests().Outcome(journal.OutcomeUnmatched).Never(t)
```

```go [gRPC]
m := mockgrpc.MustNew(mockgrpc.Setup().Journal(""))
m.MustStart()
t.Cleanup(m.Close)

// ... the code under test calls the server ...

m.VerifyRequests().Path(match.Equal("/helloworld.Greeter/SayHello")).
	Body(match.JSONPath("$.name", match.Equal("bob"))).
	Times(t, 1)
```

```sh [Admin API]
curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"method": "POST", "path": "/orders", "count": 1}' \
  http://127.0.0.1:8081/requests/verify
# {"passed": true, "count": 1, "waited_ms": 0}
```

:::

`journal` is `github.com/caffeine-projects/vlatte/journal`, and `match` is `github.com/caffeine-projects/vlatte/match`. The checks live in package `github.com/caffeine-projects/vlatte/verify`, which a server embeds when its setup keeps a journal: a test calls them on the server.

## How it works

1. **Select.** `VerifyRequests()` selects every request the journal holds. Criteria narrow the selection: the method, path, headers, body, status, outcome, the mock that answered, or the request part of a mock.
2. **Check.** `Times`, `AtLeast`, `AtMost`, `Never` and `CountIs` assert how many requests the selection holds, and `verify.InOrder` asserts the order of several selections. A check runs now, or waits for its count without polling.
3. **Report.** A failure reports with `t.Errorf` and returns `false`. It lists the requests the selection holds, and those that came closest, a line each, without headers or bodies, which may hold secrets.

A check never passes on part of the requests: one whose requests the journal dropped or cleared fails with an error, rather than count what is left.

| Page | What it covers |
|---|---|
| [Selecting requests](/verify/selecting) | The criteria, request patterns, and how gRPC calls are selected. |
| [Counts and reports](/verify/counting) | The assertions, the failure report, and reading the requests. |
| [Waiting](/verify/waiting) | Checking a request the code under test sends in the background, or that none comes for a while. |
| [Marks and order](/verify/marks-and-order) | Counting a test's own requests on a shared server, and checking their order. |
| [Admin API and MCP](/verify/admin-api) | The same checks for a client in any language, or an AI agent. |
| [Troubleshooting](/verify/troubleshooting) | The errors of a check that can't run, and what to do about each. |

The [Request journal](/features/journal) page covers the journal itself: what each entry holds, where it is kept, and its limits.
