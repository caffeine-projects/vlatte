# Numbers, dates and times

## Numbers

| Go | Mock file | Matches |
|---|---|---|
| `GreaterThan(n)`, `GT` | `greater`, `gt` | A number, or text that reads as one, greater than `n`. |
| `GreaterThanOrEqual(n)`, `GTE` | `greatereq`, `gte` | Greater than or equal to `n`. |
| `LessThan(n)` | `less`, `lt` | Less than `n`. |
| `LessThanOrEqual(n)`, `LTE` | `lesseq`, `lte` | Less than or equal to `n`. |

## Dates and times

`[before, x]`, `[after, x]` and `[equalDateTime, x]` (`eqdt`) compare a date and time in RFC 3339, in ISO 8601 without a zone, or in an HTTP format. `x` is one of those, or `now`, and either may end with an offset: `"now +3 days"`, `"2026-01-01T00:00:00Z -2 weeks"`.

Options: `truncate_expected` and `truncate_actual`, each one of `first minute of hour`, `first hour of day`, `first day of month`, `first day of next month`, `last day of month`, `first day of year`, `first day of next year` and `last day of year`; `truncate_last: true` to offset first; `actual_format` with a Go layout, such as `02/01/2006`; `local_time: true` to compare wall clocks. An `x` without a zone is local, as with `local_time`; a request's date and time without a zone, against an `x` with one, is read in the server's zone.

In Go: `Before`, `After`, `EqualDateTime`, `BeforeNow`, `AfterNow` and `EqualDateTimeNow`, with typed options such as `ExpectedOffset(3, match.Days)`.

```yaml
request:
  header:
    X-Date: [after, "now -1 hours"]
  query:
    until: [before, "now +15 days", {truncate_expected: first day of month}]
```
