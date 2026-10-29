# Equality and text

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

## Presence

| Go | Mock file | Matches |
|---|---|---|
| `Present()` | `present` | A value that is there. `[not, present]` matches a header the request doesn't have. |
| `Nil()` | `nil` | Nil. |
| `Empty()` | `empty` | Nil, and text, a list or an object of no items. |
| `Truthy()`, `Falsy()` | `truthy`, `falsy` | A value that reads as true, or false, as `strconv.ParseBool` reads text. |
| `Anything()` | `anything` | Anything. |

## URLs

| Go | Mock file | Matches |
|---|---|---|
| `URLPath(glob)`, `URLPathf`, `URLPathMatch(m)` | `urlpath` | The path of a URL. |
| `RequestURI(uri)` | `requesturi` | A URL whose path and query are those of `uri`, the parameters in any order. |
