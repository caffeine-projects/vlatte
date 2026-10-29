# Request journal

A server can keep a journal of the requests its mocks get, whatever their outcome: a mock answered it (`matched`), none matched (`unmatched`), another server did (`proxied`), serving it failed (`failed`), or its client left first (`abandoned`). A test then [verifies the requests](/verify/) it sent, while matching and hits work as before. Each entry has the status the client got: the one a reply that writes its own answer sent, as `Handler` does, `200` when it sent none, and for a failure the status sent before it, else `error_status_code`, as for a panic. A middleware (`Use`) or the decorator (`HandlerDecorator`) that answers before the mock, in its place, or once a timeout ends it, sets that status, as it does in the logs and the admin API's events.

```go
m := mockhttp.MustNew(mockhttp.Setup().Journal("")) // "" keeps it in a temporary directory, which Close removes
m.MustStart()
t.Cleanup(m.Close)

// ... the code under test sends its requests ...
// journal is github.com/caffeine-projects/vlatte/journal.

m.VerifyRequests().Method("POST").Path(match.Equal("/orders")).Times(t, 1)
m.VerifyRequests().Header("Authorization", match.HasPrefix("Bearer ")).AtLeast(t, 2)
m.VerifyRequests().Outcome(journal.OutcomeUnmatched).Never(t)

// The request part of a mock selects as a mock matches: query parameters, forms, files, the parsed body.
m.VerifyRequests().Matching(mockhttp.Request().Query("page", match.Equal("2"))).Times(t, 1)

// The journal itself pages through the requests, newest first.
page, err := m.Journal().Find(ctx, journal.Query{Path: match.GlobMatch("/users/*"), Limit: 10})
```

[Verifying requests](/verify/) covers the checks: their criteria, failure reports, waits, marks and order, and the admin API's `QUERY /requests/verify`.

An entry's `Message` tells what failed: the request, or else the on_complete events of a request a mock answered, one line each, such as a [webhook](/webhooks/) whose target answered `500`, so a test checks a webhook failed without reading the logs. A request is journaled as the server ends its answer, as hits count: a client gets a small answer only then, while it may read the start of a large one a moment before, so a check of a request sent in the background [waits](/verify/waiting).

`m.Journal()` is the journal itself. A `journal.Query`, of package `github.com/caffeine-projects/vlatte/journal`, selects by the fields every protocol has, as the criteria of a check do. `Kind`, `Outcome`, `MockID`, `Method`, `Path` and `Status` are indexed: a query runs each of their matchers once per value the journal holds, not once per request, and reads no request from disk for them. `Target` (the full URL), `Since` and `Until` filter in memory too. `Header`, `Body` and request patterns read the requests the other fields leave. `Limit` and `Before` page through them, newest first, and `After` leaves out those up to a mark; `m.Journal().Find` returns the next page's cursor and the count. A `journal.Journal` works on its own too, as a changelog of any requests: `journal.New(journal.Options{Dir: dir})`.

Patterns are the request part of a mock builder, such as `mockhttp.Request()`, which `Matching` takes: the server rebuilds each request from the journal and matches it, through its body parsers. A pattern has no `Times` or `Once`, which count served requests, nor scenario, which moves when its mock is picked, rather than match one, and no `ClientIP`: a rebuilt request has no `RemoteAddr`, nor TLS state for a `RequestMatches` predicate.

## Files

The journal is a changelog: each request is a JSON line, appended to `journal-NNNNNN.log` files. A new file starts every quarter of `journal.max_entries` requests (10 000 at most) or 64 MiB, so dropping the oldest requests removes whole files. Each full file, and the last one on a clean stop, gets a `.idx` sidecar with its index, so a restart reads no log. A text body stays text; another is `body_base64`. `DELETE /requests` and `Clear` write a `journal.clear` tombstone before they delete the files, so a restart drops a file a crash or a failed delete left. Only the user reads the files.

## Persistence

A named directory keeps the requests across restarts: a server that starts again finds the requests it served before, and a check without a mark counts them too, so tests use `Journal("")`, and admin API clients a mark or a reset first. One server at a time keeps a journal in a directory: another fails to start. An HTTP server's relative directory starts at `root_dir`, and `{workdir}` at its [work directory](/run/configuration#work-directory), as in `{workdir}/_journal`. Close closes the journal: verify the requests before it, as `t.Cleanup(m.Close)` leaves a test to.

## Limits

- The journal keeps the newest 10 000 requests (`journal.max_entries`, `0` for all), and 1 MiB of each body (`journal.max_body_size`, `0` for whole bodies).
- A body the journal cut, or that the server didn't read, has `body_truncated`: the server reads no body of a `GET` or `HEAD`, nor under `no_body_parsing`, nor past `MaxBodyParsingLimit`. A request pattern that reads the body, such as `Body`, `FormFile` or `RequestMatches`, which gets the whole request, fails on such a request, when the rest of the pattern matches it, rather than miss it: `Never` can't pass for a request the server got.
- The headers the server redacts in its logs (`RedactHeader`) are redacted in the journal. gRPC metadata is kept as it came.
- Requests the server answers before its mocks get them, such as CORS preflights, or that a middleware answers, aren't journaled: `Never` passes for them.
- A named directory gets a `.gitignore` of its files, so committing `testdata` leaves the journal out.

The settings `journal` (`--journal`, `VLATTE_JOURNAL`), `journal.dir`, `journal.max_entries` and `journal.max_body_size` configure it.

Through the [admin API](/admin-api/), `QUERY /requests` takes a query in the body, with the matchers of mock files, and answers the requests, newest first, their `count`, and `mark`, the number of the newest request of the journal. `"limit": 0` answers the count alone. [`QUERY /requests/verify`](/verify/admin-api) checks them, as a Go test does. `request` is the request part of a mock file:

```sh
curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"method": "POST", "path": "/orders", "request": {"query": {"page": "2"}}, "limit": 0}' \
  http://127.0.0.1:8081/requests
# {"count": 1, "requests": [], "mark": 41}
```

| Key | Selects |
|---|---|
| `kind`, `outcome` | The protocol, such as `http`, and the outcome, exactly. |
| `mock_id`, `method`, `path`, `target`, `status`, `body` | By a matcher: text matches equal text, a number or a bool an equal value, and a list is a matcher, such as `["glob", "/users/*"]` or `["jsonpath", "$.id", ["equal", 7]]`. |
| `header` | Headers, or metadata, by name in any case, each by a matcher. |
| `since`, `until` | The time of the requests, in RFC 3339. |
| `request` | The request part of a mock file of the server's protocol. HTTP only. Send `method` and `path` on their own too: the index narrows the requests before `request` reads them. |
| `limit`, `before` | The page: 100 requests by default, 1000 at most, and the `next` of the page before. |
| `after` | The requests after a `mark`. |

## In gRPC

gRPC servers keep the same journal with `mockgrpc.Setup().Journal(dir)`, and [verify](/verify/selecting#grpc-calls) it the same way: a call's path is its full method, such as `/pkg.Service/Method`, its headers its metadata, its body its request message as JSON, which `match.Field` and `match.JSONPath` read, and its status its code, the one the client got, after every interceptor of `ServerOptions`. Its answer's `vlatte-request-id` header names its entry. A client or bidi stream's body is the JSON array of the messages its client sent. Patterns are gRPC mock builders, such as `mockgrpc.Method("Greetings").Field("message", match.Equal("hi"))`, and the admin API's `request` key takes the request part of a gRPC mock file.

- With a journal, a stream interceptor goes through `grpc.ChainStreamInterceptor`: the journal takes `grpc.StreamInterceptor`, which grpc runs before every other, and `New` fails with `mockgrpc.ErrStreamInterceptorSlot` when `ServerOptions` sets it. A unary interceptor goes either way.
- A call an interceptor answers itself isn't journaled, nor one grpc answers before any interceptor, such as one whose request it can't decode. A call of a method no descriptor describes is journaled as unmatched.
- A unary reply grpc fails to send once every interceptor returned, such as one larger than a `grpc.MaxSendMsgSize` set, keeps the code they returned.
- A service registered on `GRPCServer()`, past the server, has its unary calls journaled within the interceptors, with the code its mocks answered with, which `Start` warns of: register services with `RegisterService`, or `pb.RegisterGreeterServer(m, ...)`.
