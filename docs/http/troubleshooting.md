# Troubleshooting

## Failures

When no mock matches, or serving a request fails, the server answers itself. It sends `418 I'm a teapot`, a status a real server rarely sends, so the answer never looks like a mock's, and the `VLatte-Error` header says why:

| `VLatte-Error` | Why |
|---|---|
| `no-match` | No mock matched the request. |
| `match` | Finding a mock failed: a matcher, or the store, returned an error. |
| `reply` | A mock matched, and its reply failed: a template, a body file, a template data provider or a response mapper. |
| `panic` | Serving the request panicked. |

The body says the same, and names the mock and the error. A test checks the header:

```go
require.Equal(t, vlatte.FailureReply, res.Header.Get(vlatte.HeaderFailure))
```

The status of `no-match` is `no_match_status_code`, and the status of the others is `error_status_code`. Both default to 418.

## Near misses

The answer to a request no mock matched lists the mocks that came closest, as does its [journal](/features/journal) entry: the parts of the request each matched, those that differ, and those it didn't check, such as a `times` not used up, which a report never runs. A `times` used up, or a scenario in another state, is a part a mock differs in.

The answer is a Markdown document, which reads the same raw, in a terminal or a test's failure, as rendered:

```md
# Request did not match

No mock matched `GET http://127.0.0.1:8080/users`.

Request ID: `0f8c2b1e-6d4a-4c3e-9a57-2b8e1d4f7c90`

## Closest mocks

1. `users` (`list users`): 2 of 3 parts matched; differs in `header Accept`
2. `create`: 1 of 2 parts matched; differs in `method`

## Next steps

- Change the request, or the closest mock, in the parts they differ in.
- See the request in the journal: `GET http://127.0.0.1:8081/requests/0f8c2b1e-6d4a-4c3e-9a57-2b8e1d4f7c90`
```

The journal line shows when the server keeps a journal and serves the [admin API](/run/admin-api), as the `vlatte` command does. Every name and value is in a code span, so none can change the structure of the document.

The report runs no matcher twice: it goes on from the part where matching stopped, which a mock differs in, so it runs only the matchers matching never reached, the action of a `match.Peek` among them. It runs none that keeps state, such as `times`, which it lists as not checked; a scenario's matcher keeps none as it matches, so it runs. A mock that wants the request but is used up, by its `Times`, differs in `times` only, and comes first. `near_misses: false`, `--near-misses=false` or `mockhttp.Setup().NoNearMisses()` turns it off.

### Formats

The application under test often asks for JSON, and fails to decode anything else. The answer follows the request's `Accept` header, and adds `Accept` to `Vary`:

| `Accept` prefers | Answer | `Content-Type` |
|---|---|---|
| `application/json`, or a JSON type such as `application/vnd.api+json` | Problem details | `application/json` |
| `application/problem+json`, over `application/json` | Problem details | `application/problem+json` |
| `text/markdown` | The Markdown document | `text/markdown; charset=utf-8` |
| Anything else, `*/*`, or no `Accept` | The Markdown document | `text/plain; charset=utf-8` |

Of the types `Accept` weighs the same, the one a more specific range names wins, then the one it lists first: `application/json, text/plain, */*` gets JSON, `*/*, application/json` gets JSON too, and a request that accepts only `*/*` gets text. `application/problem+json` wins only when `Accept` prefers it to `application/json`, by weight or by a more specific range.

Problem details are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) members, with the request and the near misses as the journal gives them:

```json
{
  "type": "https://caffeine-projects.github.io/vlatte/http/troubleshooting#near-misses",
  "title": "Request did not match",
  "status": 418,
  "detail": "No mock matched GET http://127.0.0.1:8080/users.",
  "request_id": "0f8c2b1e-6d4a-4c3e-9a57-2b8e1d4f7c90",
  "request": {"method": "GET", "url": "http://127.0.0.1:8080/users"},
  "near_misses": [
    {"mock_id": "users", "name": "list users", "passed": 2, "total": 3, "failed": ["header Accept"]}
  ]
}
```

`no_match_format`, `--no-match-format` or `mockhttp.Setup().NoMatchFormat(...)` sends one format whatever `Accept` asks: `auto`, the default, `text`, `markdown`, `json` or `problem`.

### Custom answers

A server made in Go can answer in a shape of its own, such as the error body of the API a mock stands in for. A `NoMatchRenderer` gets the request, the near misses and the status, and writes the answer; `VLatte-Error` is set before it runs, and the journal records the request whatever it sends. It can hand requests to the default renderer:

```go
type apiNoMatch struct{ fallback mockhttp.NoMatchRenderer }

func (a apiNoMatch) RenderNoMatch(w http.ResponseWriter, nm *mockhttp.NoMatch) {
	if !strings.HasPrefix(nm.Request.URL.Path, "/api/") {
		a.fallback.RenderNoMatch(w, nm)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, `{"code":"NOT_FOUND","closest":%d}`, len(nm.NearMisses))
}

m := mockhttp.MustNew(mockhttp.Setup().
	NoMatchRenderer(apiNoMatch{fallback: mockhttp.DefaultNoMatchRenderer(mockhttp.NoMatchAuto)}))
```

The server calls a renderer for many requests at once. One that writes nothing answers with the no-match status and no body. A renderer and a format other than `auto` don't go together: the format configures the default renderer, and building the server fails.

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
