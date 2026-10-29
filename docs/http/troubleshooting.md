# Troubleshooting

## Failures

When no mock matches, or serving a request fails, the server answers itself. It sends `418 I'm a teapot`, a status a real server rarely sends, so the answer never looks like a mock's, and the `VLatte-Error` header says why:

| `VLatte-Error` | Why |
|---|---|
| `no-match` | No mock matched the request. |
| `match` | Finding a mock failed: a matcher, or the store, returned an error. |
| `reply` | A mock matched, and its reply failed: a template, a body file, a template data provider or a response mapper. |
| `panic` | Serving the request panicked. |

The body says the same in text, and names the mock and the error. A test checks the header:

```go
require.Equal(t, vlatte.FailureReply, res.Header.Get(vlatte.HeaderFailure))
```

The status of `no-match` is `no_match_status_code`, and the status of the others is `error_status_code`. Both default to 418.

## Near misses

The answer to a request no mock matched lists the mocks that came closest, as does its [journal](/features/journal) entry: the parts of the request each matched, those that differ, and those it didn't check, such as a `times` not used up, which a report never runs. A `times` used up, or a scenario in another state, is a part a mock differs in.

```
REQUEST DID NOT MATCH
GET http://127.0.0.1:8080/users

CLOSEST MOCKS
[users] list users: 2 of 3 parts matched; differs in header Accept
[create]: 1 of 2 parts matched; differs in method
```

The report runs no matcher twice: it goes on from the part where matching stopped, which a mock differs in, so it runs only the matchers matching never reached, the action of a `match.Peek` among them. It runs none that keeps state, such as `times`, which it lists as not checked; a scenario's matcher keeps none as it matches, so it runs. A mock that wants the request but is used up, by its `Times`, differs in `times` only, and comes first. `near_misses: false`, `--near-misses=false` or `mockhttp.Setup().NoNearMisses()` turns it off.

## Debug headers

Each answer says how the server served it, in `VLatte-*` headers:

| Header | Value |
|---|---|
| `VLatte-Matched-Mock-Id` | The ID of the mock that matched. |
| `VLatte-Matched-Mock-Name` | Its name, when it has one. |
| `VLatte-Matched-Mock-Hits` | Its hits, this request included. |
| `VLatte-Matched-Mock-Source` | The file it was loaded from, when it was loaded from one. |
| `VLatte-Request-Id` | The ID of the request: `.Request.ID` in templates, `request_id` in the logs, its id in the [journal](/features/journal). Every answer has it, failures and no-match included. |
| `VLatte-Proxied` | `true` when a real service answered instead of a mock: a mock that replies with `From(...)`, a proxy passing on a request no mock matches, or the target of a recording. |

A failure keeps the headers of the mock that matched before it failed. Hits count once the answer is sent, so two requests served at the same time can show the same number. A test checks them like any header:

```go
require.Equal(t, "users-get", res.Header.Get(vlatte.HeaderMatchedMockID))
```

Header names don't depend on case: Go sends `Vlatte-Matched-Mock-Id`, and HTTP/2 sends them in lower case. Recordings leave the debug headers out.

`debug_headers: false` turns them all off, as do `VLATTE_DEBUG_HEADERS=false`, `--debug-headers=false` and `mockhttp.Setup().NoDebugHeaders()`. `VLatte-Error` stays: it is not a debug header.

## Logs

The server logs each request it serves, and how. `Setup().LogLevel(...)` and `Setup().LogVerbosity(...)` set what it logs, `Setup().UseDescriptiveLogger()` (`--debug` for the command) prints a readable block for each request in place of structured lines, `Setup().RedactHeader(...)` hides the values of headers, and `Setup().Silent()` logs nothing. The [admin API](/run/admin-api) streams the log lines at `GET /logs`, and what the server serves at `GET /events`.
