# Selecting requests

`m.VerifyRequests()` selects every request the journal holds, whatever its outcome. Each criterion narrows the selection, and a request must match them all:

```go
orders := m.VerifyRequests().
	Method("POST").
	Path(match.Equal("/orders")).
	Header("Content-Type", match.Contain("json")).
	Body(match.JSONPath("$.item", match.Equal("tea")))

orders.Times(t, 1)
```

| Criterion | Selects the requests |
|---|---|
| `Method(methods...)` | Of any of the methods, in any case, such as `"POST"`. |
| `Path(m)` | Whose path `m` matches: the URL's path for HTTP, the full method for gRPC. |
| `Target(m)` | Whose full target `m` matches, such as the URL with its query string. |
| `Header(name, m)` | Whose header `name`, in any case, `m` matches: its first value, or `""` for a request without it. Each name adds a header to match. gRPC calls have metadata. |
| `Body(m)` | Whose body, as text, `m` matches, such as with `match.JSONPath`. |
| `Status(m)` | Whose answer had a status `m` matches, an `int`: an HTTP status, or a gRPC code. |
| `Outcome(outcomes...)` | Of any of the outcomes: `journal.OutcomeMatched`, `OutcomeUnmatched`, `OutcomeProxied`, `OutcomeFailed` or `OutcomeAbandoned`. |
| `MockID(m)` | That a mock whose id `m` matches answered. |
| `Matching(patterns...)` | That the request part of every pattern matches: see [Request patterns](#request-patterns). |

The criteria take the [matchers](/matchers/) mocks take. They are the keys of the admin API's [queries](/verify/admin-api#keys) too.

## A selection is a value

Each method returns a new selection and leaves the one it was called on as it was. Name a selection once, and narrow it more than one way:

```go
orders := m.VerifyRequests().Method("POST").Path(match.Equal("/orders"))

orders.Times(t, 3)
orders.Body(match.JSONPath("$.item", match.Equal("tea"))).Times(t, 1)
orders.Status(match.Equal(http.StatusCreated)).Times(t, 3)
```

A criterion a selection can't take fails the check, never selects nothing: a check that selected nothing would pass `Never`. A `nil` matcher, `Method()` with no method, `Outcome()` with no outcome or with one that is none, and `Method` on a gRPC server each fail the check with why.

## Request patterns

`Matching` takes the request part of the server's own mock builders, for what no criterion says, such as an HTTP query parameter, a form field or a file. The server rebuilds each request from the journal and matches it as a mock would, through its body parsers:

```go
m.VerifyRequests().
	Matching(mockhttp.Request().Query("page", match.Equal("2"))).
	Times(t, 1)

m.VerifyRequests().
	Matching(mockhttp.Request().FormField("plan", match.Equal("pro"))).
	AtLeast(t, 1)
```

A pattern's reply counts for nothing. A pattern with `Times` or `Once`, which count served requests, a scenario, which moves when its mock is picked, or a part the journal doesn't keep, such as `ClientIP`, fails the check. A rebuilt request has no `RemoteAddr`, nor TLS state for a `RequestMatches` predicate.

Select by `Method` and `Path` next to a pattern when you can: the journal indexes them, so the pattern reads only the requests they leave.

## gRPC calls

A gRPC server keeps the same journal with `mockgrpc.Setup().Journal(dir)`, and selects its calls the same way:

- A call's path is its full method, such as `/helloworld.Greeter/SayHello`. It has no method, so select it by `Path`: `Method` fails the check with `verify.ErrNoMethod`.
- Its headers are its metadata, and its status is its code, the one the client got.
- Its body is its request message as JSON, which `match.Field` and `match.JSONPath` read. A client or bidi stream is one request, journaled as it ends, whose body is the JSON array of the messages its client sent.
- Patterns are gRPC mock builders, such as `mockgrpc.Method("SayHello").Field("name", match.Equal("bob"))`.

```go
m.VerifyRequests().
	Path(match.Equal("/helloworld.Greeter/SayHello")).
	Header("authorization", match.HasPrefix("Bearer ")).
	Status(match.Equal(int(codes.OK))).
	Times(t, 1)
```
