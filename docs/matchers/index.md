# Matchers

The `match` package holds the matchers every protocol shares. A mock's builder methods, such as `Header`, `Query` or `Field`, take one for the part of the request they read; [request journal](/more/journal) queries take them too.

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

| Page | Matchers |
|---|---|
| [Equality and text](/matchers/equality-text) | Equality, text, presence and URLs. |
| [Numbers, dates and times](/matchers/numbers-dates) | Comparisons of numbers, and of dates and times, with offsets and truncation. |
| [Lists and objects](/matchers/lists-objects) | Length, items, keys, and every or some item. |
| [JSON and XML](/matchers/json-xml) | JSONPath, JSON equality and JSON Schema; XML equality and XPath; JWT claims and signature verification. |
| [Combining](/matchers/combining) | `All`, `Any`, `Not` and the rest, and the matchers only Go has. |
| [Custom matchers](/matchers/custom) | Matchers of your own, in Go and in mock files. |
| [All matchers](/matchers/reference) | Every matcher, its Go name and its mock-file name, in one table. |
