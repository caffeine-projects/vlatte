# Mock lifecycle

These features work the same on HTTP and gRPC mocks, in Go and in mock files.

| Go | Mock file | What it does |
|---|---|---|
| `ID(id)` | `id` | Names the mock in the admin API, as in `/mocks/{id}`, the logs and the debug headers. Without one, the server gives it an ID. |
| `Name(name)` | `name` | A readable name, in the logs, the near misses and the debug headers. |
| `Priority(p)` | `priority` | Mocks with a lower priority are matched first; mocks of equal priority, in the order they were added. |
| `Enabled(false)` | `enabled: false` | Starts the mock disabled. |
| `Times(n)`, `Once()` | `repeat: n` | Serves `n` requests, then stops matching. |
| `Delay(d)` | `delay` | Waits `d` before replying. |
| `DelayFunc(fn)` | | Picks the delay of each reply, such as `vlatte.Jitter(avg, deviation)`, where deviation is a percent of avg from 0 to 200, `vlatte.Latency(d)` or `vlatte.UniformDistribution(min, max)`. |

```go
m.MustMock(mockhttp.Getf("/users").
	ID("users-list").Name("list users").
	Priority(1).Times(3).
	DelayFunc(vlatte.Jitter(200*time.Millisecond, 20)).
	Reply(mockhttp.OK().JSON([]any{})))
```

```yaml
id: users-list
name: list users
priority: 1
repeat: 3
delay: 200ms
request: {method: GET, path: /users}
response: {status: 200, body: []}
```

## Times

`Times` counts per mock: a mock built again, as `Remock` does, starts from zero. A mock used up by its `Times` no longer matches, and a request it would have answered shows it first in the [near misses](/http/troubleshooting#near-misses), as differing in `times` only. A gRPC answer with a status counts as the mock's answer, for `Times` too.

## Delays

A mock's delay comes before its reply: a client that leaves during it gets nothing, and a [proxy reply](/http/proxying)'s target isn't called. Durations in mock files are whole milliseconds (`1500`) or a Go duration (`1.5s`). For delays inside an answer, such as between the parts of a body, see [Steps](/http/steps) and [Faults](/http/faults) for HTTP, and `MessageDelay` for [gRPC streams](/grpc/streaming).

## Enabling and disabling

A scope's `Enable()` and `Disable()` turn its mocks on and off, and the server's turn every mock. The [admin API](/run/admin-api) does it with `PATCH /mocks/{id}` and `{"enabled": false}`.

## Priorities and proxies

A mock that proxies every request needs a priority number above 0 to let other mocks answer first: `AnyMethod().Priority(10).Reply(From(target))`. `ProxyAll` mocks come last, at priority `math.MaxInt`.
