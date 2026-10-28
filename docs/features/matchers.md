# Matchers

The `match` package holds the matchers every protocol shares. A mock's builder methods, such as `Header`, `Query` or `Field`, take one for the part of the request they read; [request journal](/features/journal) queries take them too.

```go
mockhttp.Getf("/users").Query("page", match.GreaterThan(0))
mockgrpc.Method("users.Users/Get").Field("id", match.HasPrefix("u-"))
```

In a mock file, a matcher is a list whose first item names it and whose other items are its arguments: `[contains, x]`, `[jsonpath, $.a, [gt, 1]]`. Names ignore case, so `hasPrefix` is `hasprefix`. Where a request's value goes, such as a header or a query parameter, text alone matches equal text: `page: "2"` is `page: [equal, "2"]`.

## How values compare

Every matcher compares values by one rule, the same that mock files and settings read values by. A value has a class: nil, a bool, text, a number, a list or an object.

- Values of one class compare by value. Numbers are equal when their exact values are, whatever their Go types: `10`, `int64(10)` and `10.0` are equal.
- Most of a request is text: its path, query, headers and form values, and a body that isn't JSON. So `Equal`, and the matchers built on it, `IsIn`, `Contain` and `ItemsMatch`, read text as the class they compare with:
  - text compared with a number reads as a number when it is written in the JSON number grammar: `Equal(10)` matches the query value `"10"` and `"1e1"`, but not `"010"`, `"+10"` or `" 10"`;
  - anything compared with a bool reads as one the way `strconv.ParseBool` reads text: `Equal(true)` matches `true`, `"true"`, `"t"`, `"1"` and the number `1`.
- Nothing else crosses classes: text never equals a list. `StrictEqual` and `EqualJSON` read no text at all.

The [package docs](https://pkg.go.dev/github.com/caffeine-projects/vlatte/match) state the rule in full.

## Equality

| Go | Mock file | Matches |
|---|---|---|
| `Equal(v)`, `Eq` | `equal`, `eq` | A value equal to `v`, by the rule above. |
| `StrictEqual(v)`, `EQS` | `equalstrict`, `eqs` | A value equal to `v`, reading no text. |
| `EqualIgnoreCase(s)`, `EQI` | `equalignorecase`, `eqi` | Text equal to `s`, ignoring case. |
| `IsIn(items)` | `isin` | A value equal to one of `items`: `[isIn, [10, 20]]`. |
| `EqualBytes(b)` | `equalbytes` | Bytes equal to `b`, given as standard base64 in a mock file. |

## Text

| Go | Mock file | Matches |
|---|---|---|
| `Contain(v)` | `contains` | Text holding `v`, a list holding an item equal to `v`, or an object holding the key `v`. |
| `HasPrefix(s)`, `HasSuffix(s)` | `hasprefix`, `hassuffix` | Text that starts or ends with `s`. |
| `GlobMatch(pattern)` | `glob` | Text a glob matches, such as `/users/*`. |
| `RegExpr(expr)` | `regex`, `regexp` | Text a Go regular expression matches. |
| `Trim(m)` | `trim` | Text that `m` matches once its spaces are trimmed. |
| `ToLower(m)`, `ToUpper(m)` | `lowercase`, `uppercase` | Text that `m` matches once lowered or uppercased. |
| `Split(sep, m)` | `split` | Text that `m` matches as the list of its parts: `[split, ",", [len, 3]]`. |

## Numbers

| Go | Mock file | Matches |
|---|---|---|
| `GreaterThan(n)`, `GT` | `greater`, `gt` | A number, or text that reads as one, greater than `n`. |
| `GreaterThanOrEqual(n)`, `GTE` | `greatereq`, `gte` | Greater than or equal to `n`. |
| `LessThan(n)` | `less`, `lt` | Less than `n`. |
| `LessThanOrEqual(n)`, `LTE` | `lesseq`, `lte` | Less than or equal to `n`. |

## Presence

| Go | Mock file | Matches |
|---|---|---|
| `Present()` | `present` | A value that is there. `[not, present]` matches a header the request doesn't have. |
| `Nil()` | `nil` | Nil. |
| `Empty()` | `empty` | Nil, and text, a list or an object of no items. |
| `Truthy()`, `Falsy()` | `truthy`, `falsy` | A value that reads as true, or false, as `strconv.ParseBool` reads text. |
| `Anything()` | `anything` | Anything. |

## Lists and objects

| Go | Mock file | Matches |
|---|---|---|
| `Len(n)` | `len`, `length` | Text of `n` bytes, or a list or an object of `n` items. |
| `Each(m)` | `each` | A list whose items, or an object whose members, all match `m`. |
| `Some(m)` | `some` | A list with an item that matches `m`. |
| `Item(i, m)` | `item` | A list whose item at `i` matches `m`. |
| `ItemsMatch(items)` | `itemsmatch` | A list holding `items` in any order, and nothing else. |
| | `hasexactly` | A list of as many values as matchers, each matched by a matcher of its own, in any order: `[hasExactly, [[equal, "1"], [contains, "2"]]]`. In Go, `ItemsMatch` takes matchers as items. |
| `HasKey(path)` | `haskey` | An object that has the key, or JSON path. |

## JSON and XML

| Go | Mock file | Matches |
|---|---|---|
| `JSONPath(path, m)`, `Field` | `jsonpath`, `field` | What a JSONPath selects, matched by `m`: `[jsonpath, $.id, [gt, 1]]`. |
| `EqualJSON(doc, opts...)`, `EQJ` | `equaljson`, `eqj` | JSON equal to `doc`. Options loosen it: see [JSON bodies](/http/matching#json-bodies). |
| `JSONSchema(schema, opts...)` | `jsonschema` | JSON that conforms to a JSON Schema. |
| `EqualXML(doc, opts...)`, `EQX` | `equalxml`, `eqx` | An equal XML document, by what it means: see [XML bodies](/http/matching#xml-bodies). |
| `XPath(expr, m, opts...)` | `xpath` | What an XPath 1.0 expression selects, matched by `m`. |

## URLs

| Go | Mock file | Matches |
|---|---|---|
| `URLPath(glob)`, `URLPathf`, `URLPathMatch(m)` | `urlpath` | The path of a URL. |
| `RequestURI(uri)` | `requesturi` | A URL whose path and query are those of `uri`, the parameters in any order. |

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

## Combining

| Go | Mock file | Matches |
|---|---|---|
| `All(ms...)` | `all` | What every matcher matches: `[all, [[gt, 1], [lt, 9]]]`. |
| `Any(ms...)` | `any` | What some matcher matches. |
| `Both(a, b)`, `Either(a, b)`, `XOR(a, b)` | `both`, `either`, `xor` | What both, either, or exactly one of two matchers match. |
| `Not(m)` | `not` | What `m` doesn't match. |

## In Go only

| Matcher | Matches |
|---|---|
| `Function(fn)`, `Predicate(fn)` | What a function reports true for. |
| `Peek(m, action)` | What `m` matches, running `action` on the value first, such as to log it. |

To use a matcher of your own in mock files, register it: see [Custom matchers](/features/custom-matchers).
