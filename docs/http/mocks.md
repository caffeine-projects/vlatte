# Mocks

A mock is built with a `*mockhttp.Builder` and added to a server with `m.Mock` or `m.MustMock`, which return a scope of the mocks they added.

```go
scoped := m.MustMock(
	mockhttp.Getf("/users/%d", 1).Reply(mockhttp.OK().JSON(map[string]any{"id": 1})),
	mockhttp.Postf("/users").Reply(mockhttp.Created()),
)

// ... the code under test calls the server ...

scoped.AssertCalled(t)
```

## Starting a mock

| Function | Starts a mock that matches |
|---|---|
| `Getf(path, a...)`, `Postf`, `Putf`, `Patchf`, `Deletef`, `Headf` | That method, and a path glob formatted as `fmt.Sprintf` formats it. |
| `Get(m)`, `Post(m)`, `Put(m)`, `Patch(m)`, `Delete(m)`, `Head(m)` | That method, and a URL matcher, such as `match.URLPath("/users")`. |
| `AnyMethod()` | Any method, and any path. |
| `Request()` | Nothing yet: add matchers yourself. Also the request part of a [record filter](/recording/) and of a [journal query](/more/journal). |
| `ProxyAll(From(target))` | What no other mock matches: see [Proxying](/http/proxying#proxy-all). |
| `FromFile(path)`, `FromBytes(data, format)` | What a [mock file](/http/mock-files) says. |

Then chain [matchers](/http/matching), a [reply](/http/replies), and the mock features below.

## Mock features

| Method | What it does |
|---|---|
| `ID(id)`, `Name(name)` | Name the mock, in the admin API, the logs and the debug headers. |
| `Priority(p)` | When several mocks match, the lowest number answers. |
| `Enabled(false)` | Start the mock disabled: its scope's `Enable`, or the admin API, enables it. |
| `Times(n)`, `Once()` | Serve a number of requests, then stop matching. See [Mock lifecycle](/more/mock-lifecycle). |
| `Delay(delay)` | Wait before replying. |
| `StartScenario`, `ScenarioIs`, `ScenarioStateIs`, `ScenarioStateWillBe`, `ScenarioKey` | Take part in a [scenario](/scenarios/). |
| `OnCompleteEvent(cfg)`, `Callback(fn)` | Run [events such as webhooks](/webhooks/), or a function, once the mock replied. |
| `Map(mapper)` | Change the response after it was built. A mapper finds the body in `Body`: one that streams, such as a proxy's, is read whole for it first. |
| `Pipe`, `WirePipe`, `UpstreamPipe` | Pass the answer through [pipes](/http/pipes), such as [faults](/http/faults). |

## Adding, reloading and removing

- `m.Mock(builders...)` adds mocks and returns their scope, or an error when one fails to build. `m.MustMock` panics instead.
- `m.Unmock(ctx, scope)` removes the mocks of a scope, `m.Remock(ctx, old, builders...)` replaces them, and `m.Clean(ctx)` removes every mock.
- A server loads [mock files](/http/mock-files) on start. `m.Reload()` replaces the mocks of the files with what they hold now, in one step. When a file has problems, it returns them and the mocks stay as they were.
- The [admin API](/admin-api/) adds, replaces, enables, disables and removes mocks while the server runs.

## Asserting

A scope's assertions check how its mocks were called, and the server's how all of its mocks were:

| Method | Checks |
|---|---|
| `AssertCalled(t)`, `AssertNotCalled(t)` | Every mock was called, or none was. |
| `AssertNumberOfCalls(t, n)` | The mocks were called `n` times in all. |
| `Called()`, `Hits()`, `Pending()` | The same, as values: whether they were called, how many times, and whether some wasn't. |
| `ListCalled()`, `ListPending()` | The mocks that were called, and those that weren't. |

To check the requests themselves, such as their headers or bodies, keep a [request journal](/more/journal).
