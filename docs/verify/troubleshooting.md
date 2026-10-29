# Troubleshooting

A check never passes or fails on part of the requests it counts. When it can't count them all, or can't run at all, it fails with an error that says why, which an assertion reports with `t.Errorf`, and `Find` and `Count` return. The errors are in package `verify`, unless named otherwise.

## The journal dropped requests

```text
verify: the journal dropped requests the check counts: raise its MaxEntries: 1200 of them
```

`ErrDropped`. The journal keeps the newest 10 000 requests by default, and drops the oldest to keep that many. Requests the check would count may be among those dropped, so a count of the rest could pass when it shouldn't, or `Never` miss a request the server got.

A check without a mark counts from the start of the journal, so on a server that has dropped requests it always fails this way. Either:

- Take a mark before the test and select with `Since(mark)`: the check counts from the mark, and passes as long as the journal still holds what came after it.
- Raise `journal.max_entries` (`--journal-max-entries`, or `JournalConfig.MaxEntries` in Go), or set it to `0` to keep every request.

Over the admin API, it is a `409` with the code `incomplete_journal`.

## The journal was cleared

```text
verify: the journal was cleared: the requests the check counts are gone
```

`ErrCleared`. A clear, such as `POST /reset` or `m.Journal().Clear()`, removed requests the check counts: after the check's mark, while it read the journal, or during its wait. A mark past the journal's newest request, as of a journal that started over, fails this way too.

A check without a mark, after a clear, counts what came since: a test that clears the server between its steps checks each step that way.

## A body the journal cut

```text
journal: mock: matching: entry 7: journal: the journal kept part of the body, which the query reads
```

`journal.ErrBodyCut`. The journal keeps 1 MiB of each body by default (`journal.max_body_size`). A check that reads the body, with `Body` or a pattern, can't tell whether a request it cut matched, once the rest of the check matches it: what was cut may hold what the matcher looks for. Raise `journal.max_body_size`, or `0` for whole bodies.

The server reads no body of a `GET` or `HEAD`, nor under `no_body_parsing`, nor past `MaxBodyParsingLimit`: those requests have `body_truncated` too.

## A check that can't run

| Error | Why | What to do |
|---|---|---|
| `journal.ErrNoJournal` | The server keeps no journal. | `Setup().Journal("")`. |
| `ErrNoMethod` | `Method` on a gRPC server, whose calls have none. | Select by `Path`, the full method. |
| `ErrOtherJournal` | `Since` with a mark of another server's journal. | Take the mark on the server the check reads. |
| `ErrNoRequestPatterns` | `Matching` on a server that takes no request patterns. | Select by the criteria. |
| `ErrInvalidSpec` | A check that contradicts itself, such as `Within` and `During` together, `Within` with a count that passes with no request, or an outcome that is none. | Fix the check, as the message says. |

A `nil` matcher, `Method()` with no method, `Outcome()` with no outcome and a negative count fail the check too, with a message that names them. The first misuse of a selection is the one reported.

## A request that isn't in the journal

Some requests are never journaled, so `Never` passes for them, and other checks don't count them:

- Requests the server answers before its mocks get them, such as CORS preflights, or that a middleware answers.
- gRPC calls an interceptor answers itself, or that grpc answers before any interceptor, such as one whose request it can't decode.

The [Request journal](/features/journal#limits) page lists the journal's limits.
