# All matchers

The matchers of the `match` package, by their Go name and their name in mock files, which ignores case. `Equalf`, `StrictEqualf`, `EqualIgnoreCasef` and `Containf` take their expected value as a format and its arguments. A blank mock-file name means Go only. Each row links to the page that explains it.

| Go | Mock file | Page |
|---|---|---|
| `Equal(v)`, `Eq` | `equal`, `eq` | [Equality and text](/matchers/equality-text) |
| `StrictEqual(v)`, `EQS` | `equalstrict`, `eqs` | [Equality and text](/matchers/equality-text) |
| `EqualIgnoreCase(s)`, `EQI` | `equalignorecase`, `eqi` | [Equality and text](/matchers/equality-text) |
| `IsIn(items)` | `isin` | [Equality and text](/matchers/equality-text) |
| `EqualBytes(b)` | `equalbytes` | [Equality and text](/matchers/equality-text) |
| `Contain(v)` | `contains` | [Equality and text](/matchers/equality-text) |
| `HasPrefix(s)`, `HasSuffix(s)` | `hasprefix`, `hassuffix` | [Equality and text](/matchers/equality-text) |
| `GlobMatch(pattern)` | `glob` | [Equality and text](/matchers/equality-text) |
| `RegExpr(expr)` | `regex`, `regexp` | [Equality and text](/matchers/equality-text) |
| `Trim(m)` | `trim` | [Equality and text](/matchers/equality-text) |
| `ToLower(m)`, `ToUpper(m)` | `lowercase`, `uppercase` | [Equality and text](/matchers/equality-text) |
| `Split(sep, m)` | `split` | [Equality and text](/matchers/equality-text) |
| `Present()` | `present` | [Equality and text](/matchers/equality-text) |
| `Nil()` | `nil` | [Equality and text](/matchers/equality-text) |
| `Empty()` | `empty` | [Equality and text](/matchers/equality-text) |
| `Truthy()`, `Falsy()` | `truthy`, `falsy` | [Equality and text](/matchers/equality-text) |
| `Anything()` | `anything` | [Equality and text](/matchers/equality-text) |
| `URLPath(glob)`, `URLPathf`, `URLPathMatch(m)` | `urlpath` | [Equality and text](/matchers/equality-text) |
| `RequestURI(uri)` | `requesturi` | [Equality and text](/matchers/equality-text) |
| `GreaterThan(n)`, `GT` | `greater`, `gt` | [Numbers, dates and times](/matchers/numbers-dates) |
| `GreaterThanOrEqual(n)`, `GTE` | `greatereq`, `gte` | [Numbers, dates and times](/matchers/numbers-dates) |
| `LessThan(n)` | `less`, `lt` | [Numbers, dates and times](/matchers/numbers-dates) |
| `LessThanOrEqual(n)`, `LTE` | `lesseq`, `lte` | [Numbers, dates and times](/matchers/numbers-dates) |
| `Before(t)`, `BeforeNow()` | `before` | [Numbers, dates and times](/matchers/numbers-dates#dates-and-times) |
| `After(t)`, `AfterNow()` | `after` | [Numbers, dates and times](/matchers/numbers-dates#dates-and-times) |
| `EqualDateTime(t)`, `EqualDateTimeNow()` | `equaldatetime`, `eqdt` | [Numbers, dates and times](/matchers/numbers-dates#dates-and-times) |
| `Len(n)` | `len`, `length` | [Lists and objects](/matchers/lists-objects) |
| `Each(m)` | `each` | [Lists and objects](/matchers/lists-objects) |
| `Some(m)` | `some` | [Lists and objects](/matchers/lists-objects) |
| `Item(i, m)` | `item` | [Lists and objects](/matchers/lists-objects) |
| `ItemsMatch(items)` | `itemsmatch` | [Lists and objects](/matchers/lists-objects) |
| `ItemsMatch` with matchers | `hasexactly` | [Lists and objects](/matchers/lists-objects) |
| `HasKey(path)` | `haskey` | [Lists and objects](/matchers/lists-objects) |
| `JSONPath(path, m)`, `Field` | `jsonpath`, `field` | [JSON and XML](/matchers/json-xml) |
| `JWT(m)` | `jwt` | [JSON and XML](/matchers/json-xml#jwts) |
| `SignedJWT(keys, m)` | `signedjwt` | [JSON and XML](/matchers/json-xml#jwts) |
| `EqualJSON(doc, opts...)`, `EQJ` | `equaljson`, `eqj` | [JSON and XML](/matchers/json-xml) |
| `JSONSchema(schema, opts...)` | `jsonschema` | [JSON and XML](/matchers/json-xml) |
| `EqualXML(doc, opts...)`, `EQX` | `equalxml`, `eqx` | [JSON and XML](/matchers/json-xml) |
| `XPath(expr, m, opts...)` | `xpath` | [JSON and XML](/matchers/json-xml) |
| `All(ms...)` | `all` | [Combining](/matchers/combining) |
| `Any(ms...)` | `any` | [Combining](/matchers/combining) |
| `Both(a, b)`, `Either(a, b)`, `XOR(a, b)` | `both`, `either`, `xor` | [Combining](/matchers/combining) |
| `Not(m)` | `not` | [Combining](/matchers/combining) |
| `Function(fn)`, `Predicate(fn)` |  | [Combining](/matchers/combining#in-go-only) |
| `Peek(m, action)` |  | [Combining](/matchers/combining#in-go-only) |
