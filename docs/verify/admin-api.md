# Admin API and MCP

A client in any language, or an AI agent, checks the requests as a Go test does, through the [admin API](/admin-api/). `QUERY /requests/verify` takes a check in the body and answers its verdict, with the report a Go test gets. One engine runs both, so a check gives the same verdict from Go and from the admin API.

```sh
curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"method": "POST", "path": "/orders", "count": 1, "within": "2s"}' \
  http://127.0.0.1:8081/requests/verify
# {"passed": true, "count": 1, "waited_ms": 12}
```

`QUERY` is the HTTP [`QUERY`](https://httpwg.org/http-extensions/draft-ietf-httpbis-safe-method-w-body.html) method, safe as `GET` is, with a body: `curl -X QUERY`.

## Keys

A check takes the keys of `QUERY /requests`, with the matchers of mock files, to select the requests, and these to check them:

| Key | What it does |
|---|---|
| `kind`, `outcome`, `mock_id`, `method`, `path`, `target`, `status`, `body`, `header`, `since`, `until`, `request` | Select the requests, as for [`QUERY /requests`](/more/journal). `limit` and `before` don't apply: a check counts all it selects. |
| `count` | The number of requests: a number, or a matcher list, such as `["gte", 2]` or `["lte", 3]`. None is at least 1. |
| `within` | Waits up to this long for the count to pass, such as `"2s"`, as [`Within`](/verify/waiting#within) does. |
| `during` | Holds the count this long, such as `"2s"`, as [`During`](/verify/waiting#during) does. |
| `after` | Checks the requests after this mark only: see [Marks](#marks). |
| `in_order` | A list of steps, each the selecting keys of a query, to check an [order](/verify/marks-and-order#order) in place of a count. |

A wait is at most 30 seconds, as the client holds its connection all along: check again for a longer one. `within` and `during` don't go together, nor `within` with a count that passes with no request, nor `in_order` with `count` or a wait. An order's steps take no `after` of their own: the check's `after` holds for each.

```sh
curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"in_order": [{"path": "/login"}, {"path": "/orders"}, {"path": "/logout"}]}' \
  http://127.0.0.1:8081/requests/verify
# {"passed": true, "waited_ms": 0}
```

## The verdict

A check that ran answers `200`, whether it passed or not: a failed check is an answer, not an error.

| Field | What it holds |
|---|---|
| `passed` | Whether the check passed. |
| `count` | How many requests it selected. An order has none. |
| `waited_ms` | How long it waited, in milliseconds. |
| `report` | Why it failed, as a Go test's report says it. |
| `selected` | The requests it selected, the newest first, ten at most. |
| `closest` | When it wanted more requests, those that came closest, with `passed` and `total`, the parts each passed of those checked, and `missed`, the names of those it missed. |

A listed request has its `seq`, its number in the journal, its `id`, and its `line`, without its headers or body: `GET /requests/{id}` answers the rest of it.

```json
{
  "passed": false,
  "count": 0,
  "waited_ms": 0,
  "report": "\nExpected exactly 1 request(s) with method, path.\nGot 0.\n ...",
  "closest": [
    {"seq": 2, "id": "c6f3…", "line": "#2 2026-10-29T14:05:55.699Z unmatched POST http://127.0.0.1:34207/order 418", "passed": 1, "total": 2, "missed": ["path"]}
  ]
}
```

## Marks

A client takes a mark before its test, and sends it as `after`: the requests up to it are other tests'. `GET /requests` and `QUERY /requests` answer `mark`, the number of the newest request of the journal, and `GET /requests?limit=0` answers it alone:

```sh
curl http://127.0.0.1:8081/requests?limit=0
# {"count": 41, "requests": [], "mark": 41}

# ... the test sends its requests ...

curl -X QUERY -H 'Content-Type: application/json' \
  -d '{"path": "/orders", "after": 41, "count": 1}' \
  http://127.0.0.1:8081/requests/verify
```

`after` works on `GET /requests` (`?after=41`) and `QUERY /requests` too, to list the requests after a mark.

## Errors

A check that can't run answers a [problem document](/admin-api/), never a verdict, as a check that selected nothing could pass:

| Status | Code | When |
|---|---|---|
| `400` | `invalid_body` | The check can't run: an unknown key, a matcher that doesn't exist, a negative count, a wait out of bounds, or keys that don't go together. |
| `409` | `incomplete_journal` | The journal no longer holds every request the check counts: it dropped some to keep `journal.max_entries`, it was cleared after the mark or during the wait, the mark is past its newest request, or it cut a body the check reads. See [Troubleshooting](/verify/troubleshooting). |
| `501` | `not_supported` | The server keeps no journal. |
| `503` | `closing` | The admin API or the mock server closed during the wait. |

`POST /reset` clears the journal, so it ends a wait with `409`.

## MCP

Over [MCP](/mcp/setup), the `verify_requests` tool runs the same check. Its argument is the check under `check`, and it answers the verdict:

```json
{"check": {"method": "POST", "path": "/orders", "count": 1, "within": "2s"}}
```

`list_requests` and `query_requests` take `after` too, and answer `mark`. A server without a journal has none of these tools.
